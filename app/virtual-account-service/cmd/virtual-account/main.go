package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wilebeast/local-platform-lab/app/virtual-account-service/internal/va"
)

//go:embed schema.sql
var schema string

type server struct {
	db     *pgxpool.Pool
	secret string
}

func main() {
	ctx := context.Background()
	dsn := os.Getenv("POSTGRES_DSN")
	secret := os.Getenv("PROVIDER_WEBHOOK_SECRET")
	if dsn == "" || secret == "" {
		log.Fatal("POSTGRES_DSN and PROVIDER_WEBHOOK_SECRET are required")
	}
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 30; i++ {
		if err = db.Ping(ctx); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		log.Fatal(err)
	}
	if _, err = db.Exec(ctx, schema); err != nil {
		log.Fatal(err)
	}
	s := &server{db, secret}
	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "all"
	}
	if mode == "worker" || mode == "all" {
		go s.worker(ctx)
	}
	if mode == "worker" {
		select {}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /api/v1/va/capabilities", s.capabilities)
	mux.HandleFunc("POST /api/v1/va/openings", s.createOpening)
	mux.HandleFunc("GET /api/v1/va/openings/{id}", s.getOpening)
	mux.HandleFunc("POST /api/v1/provider/events", s.providerEvent)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func (s *server) capabilities(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"products": []any{map[string]any{"code": "usd_collection", "currency": "USD", "rails": []string{"local_transfer", "wire"}, "opening_mode": "asynchronous"}}})
}
func fingerprint(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func (s *server) createOpening(w http.ResponseWriter, r *http.Request) {
	var q struct {
		IdempotencyKey, OwnerID, Currency string `json:"-"`
	}
	var raw map[string]string
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		write(w, 400, map[string]string{"error": "invalid json"})
		return
	}
	q.IdempotencyKey = raw["idempotency_key"]
	q.OwnerID = raw["owner_id"]
	q.Currency = strings.ToUpper(raw["currency"])
	if q.IdempotencyKey == "" || q.OwnerID == "" || q.Currency != "USD" {
		write(w, 400, map[string]string{"error": "invalid request"})
		return
	}
	fp := fingerprint(q.OwnerID + ":" + q.Currency)
	id := "opening-" + fingerprint(q.IdempotencyKey)[:24]
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		write(w, 500, map[string]string{"error": "begin opening transaction"})
		return
	}
	defer tx.Rollback(r.Context())
	tag, err := tx.Exec(r.Context(), `INSERT INTO va_openings(opening_id,idempotency_key,owner_id,currency,status,command_fingerprint) VALUES($1,$2,$3,$4,'requested',$5) ON CONFLICT DO NOTHING`, id, q.IdempotencyKey, q.OwnerID, q.Currency, fp)
	if err != nil {
		write(w, 500, map[string]string{"error": "create opening"})
		return
	}
	if tag.RowsAffected() == 0 {
		var oldfp string
		if err = tx.QueryRow(r.Context(), `SELECT command_fingerprint FROM va_openings WHERE idempotency_key=$1`, q.IdempotencyKey).Scan(&oldfp); err != nil {
			write(w, 500, map[string]string{"error": "read existing opening"})
			return
		}
		if oldfp != fp {
			write(w, 409, map[string]string{"error": "idempotency key belongs to another request"})
			return
		}
	} else {
		if _, err = tx.Exec(r.Context(), `INSERT INTO va_channel_operations(operation_id,opening_id,status) VALUES($1,$2,'pending')`, "operation-"+id, id); err != nil {
			write(w, 500, map[string]string{"error": "create channel operation"})
			return
		}
	}
	if err = tx.Commit(r.Context()); err != nil {
		write(w, 500, map[string]string{"error": "commit opening"})
		return
	}
	s.respondOpening(w, r, id)
}
func (s *server) getOpening(w http.ResponseWriter, r *http.Request) {
	s.respondOpening(w, r, r.PathValue("id"))
}
func (s *server) respondOpening(w http.ResponseWriter, r *http.Request, id string) {
	var owner, currency, status, account string
	err := s.db.QueryRow(r.Context(), `SELECT o.owner_id,o.currency,o.status,coalesce(a.account_id,'') FROM va_openings o LEFT JOIN va_accounts a USING(opening_id) WHERE o.opening_id=$1`, id).Scan(&owner, &currency, &status, &account)
	if err != nil {
		write(w, 404, map[string]string{"error": "not found"})
		return
	}
	rails := []map[string]string{}
	if account != "" {
		rows, queryErr := s.db.Query(r.Context(), `SELECT rail_type,routing_code,masked_account FROM va_account_rails WHERE account_id=$1 ORDER BY rail_type`, account)
		if queryErr != nil {
			write(w, 500, map[string]string{"error": "read account rails"})
			return
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c string
			if err = rows.Scan(&a, &b, &c); err != nil {
				write(w, 500, map[string]string{"error": "scan account rail"})
				return
			}
			rails = append(rails, map[string]string{"type": a, "routing_code": b, "masked_account": c})
		}
	}
	write(w, 200, map[string]any{"opening_id": id, "owner_id": owner, "currency": currency, "status": status, "account_id": account, "rails": rails})
}
func (s *server) providerEvent(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	ts, id, sig := r.Header.Get("X-Provider-Timestamp"), r.Header.Get("X-Provider-Event-Id"), r.Header.Get("X-Provider-Signature")
	if !va.Verify(s.secret, ts, id, body, sig) {
		write(w, 401, map[string]string{"error": "invalid signature"})
		return
	}
	var e struct {
		ID, Type, ProviderResourceID, Currency string `json:"-"`
		OccurredAt, AmountMinor                int64
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		write(w, 400, map[string]string{"error": "invalid event"})
		return
	}
	e.ID = fmt.Sprint(raw["id"])
	e.Type = fmt.Sprint(raw["type"])
	e.ProviderResourceID = fmt.Sprint(raw["provider_resource_id"])
	e.Currency = fmt.Sprint(raw["currency"])
	fmt.Sscan(fmt.Sprint(raw["occurred_at"]), &e.OccurredAt)
	fmt.Sscan(fmt.Sprint(raw["amount_minor"]), &e.AmountMinor)
	if e.ID != id || e.Type != "deposit.credited" || e.ProviderResourceID == "" || e.Currency != "USD" || e.AmountMinor <= 0 {
		write(w, 400, map[string]string{"error": "invalid event"})
		return
	}
	tag, err := s.db.Exec(r.Context(), `INSERT INTO va_provider_event_inbox(event_id,provider_resource_id,event_type,occurred_at,raw_body) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, id, e.ProviderResourceID, e.Type, e.OccurredAt, body)
	if err != nil {
		write(w, 500, map[string]string{"error": err.Error()})
		return
	}
	write(w, 200, map[string]any{"received": true, "duplicate": tag.RowsAffected() == 0})
}
func (s *server) worker(ctx context.Context) {
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		s.provision(ctx)
		s.processEvent(ctx)
	}
}
func (s *server) provision(ctx context.Context) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var op, id, owner, currency string
	err = tx.QueryRow(ctx, `SELECT c.operation_id,o.opening_id,o.owner_id,o.currency FROM va_channel_operations c JOIN va_openings o USING(opening_id) WHERE c.status='pending' ORDER BY c.updated_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&op, &id, &owner, &currency)
	if err != nil {
		return
	}
	p := va.Provision(id, owner, currency)
	_, err = tx.Exec(ctx, `INSERT INTO va_accounts(account_id,opening_id,owner_id,currency,provider_resource_id,status) VALUES($1,$2,$3,$4,$5,'active')`, p.AccountID, id, owner, currency, p.ProviderResourceID)
	if err != nil {
		return
	}
	for _, r := range p.Rails {
		if _, err = tx.Exec(ctx, `INSERT INTO va_account_rails(account_id,rail_type,routing_code,masked_account) VALUES($1,$2,$3,$4)`, p.AccountID, r.Type, r.RoutingCode, r.MaskedAccount); err != nil {
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE va_openings SET status='active',updated_at=now() WHERE opening_id=$1`, id); err != nil {
		return
	}
	if _, err = tx.Exec(ctx, `UPDATE va_channel_operations SET status='succeeded',attempt_count=attempt_count+1,updated_at=now() WHERE operation_id=$1`, op); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
func (s *server) processEvent(ctx context.Context) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(ctx)
	var id, provider string
	var body []byte
	err = tx.QueryRow(ctx, `SELECT event_id,provider_resource_id,raw_body FROM va_provider_event_inbox WHERE processing_status='pending' ORDER BY received_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &provider, &body)
	if err != nil {
		return
	}
	var raw map[string]any
	if json.Unmarshal(body, &raw) != nil {
		return
	}
	var amount int64
	fmt.Sscan(fmt.Sprint(raw["amount_minor"]), &amount)
	var account, currency string
	if tx.QueryRow(ctx, `SELECT account_id,currency FROM va_accounts WHERE provider_resource_id=$1 AND status='active'`, provider).Scan(&account, &currency) != nil {
		if _, err = tx.Exec(ctx, `UPDATE va_provider_event_inbox SET processing_status='dead_letter',attempt_count=attempt_count+1,last_error='account not found',processed_at=now() WHERE event_id=$1`, id); err != nil {
			return
		}
		_ = tx.Commit(ctx)
		return
	}
	payin := "payin-" + fingerprint(id)[:24]
	credit := "credit-" + fingerprint(id)[:24]
	if _, err = tx.Exec(ctx, `INSERT INTO va_payins(payin_id,event_id,account_id,amount_minor,currency,status) VALUES($1,$2,$3,$4,$5,'credited') ON CONFLICT DO NOTHING`, payin, id, account, amount, currency); err != nil {
		return
	}
	creditTag, err := tx.Exec(ctx, `INSERT INTO va_credit_entries(credit_id,payin_id,account_id,amount_minor,currency) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, credit, payin, account, amount, currency)
	if err != nil {
		return
	}
	if creditTag.RowsAffected() == 1 {
		if _, err = tx.Exec(ctx, `INSERT INTO va_balances(account_id,currency,credited_minor,version) VALUES($1,$2,$3,1) ON CONFLICT(account_id) DO UPDATE SET credited_minor=va_balances.credited_minor+EXCLUDED.credited_minor,version=va_balances.version+1,updated_at=now()`, account, currency, amount); err != nil {
			return
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE va_provider_event_inbox SET processing_status='processed',attempt_count=attempt_count+1,processed_at=now() WHERE event_id=$1`, id); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}
