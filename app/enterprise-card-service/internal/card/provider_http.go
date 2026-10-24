package card

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type HTTPProvider struct {
	baseURL string
	client  *http.Client
}

func NewHTTPProvider(baseURL string, client *http.Client) *HTTPProvider {
	return &HTTPProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *HTTPProvider) ApplyCard(ctx context.Context, input ProviderApplyRequest) (ProviderApplyResult, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/provider/cards", bytes.NewReader(body))
	if err != nil {
		return ProviderApplyResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return ProviderApplyResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return ProviderApplyResult{}, fmt.Errorf("provider status %d", response.StatusCode)
	}
	var result ProviderApplyResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return ProviderApplyResult{}, err
	}
	if result.ProviderCardID == "" {
		return ProviderApplyResult{}, errors.New("provider returned an empty card id")
	}
	return result, nil
}

func NewProviderSimulatorHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /provider/cards", func(response http.ResponseWriter, request *http.Request) {
		var input ProviderApplyRequest
		if json.NewDecoder(http.MaxBytesReader(response, request.Body, 32<<10)).Decode(&input) != nil || input.CardID == "" {
			writeError(response, http.StatusBadRequest, "card_id is required")
			return
		}
		writeJSON(response, http.StatusCreated, ProviderApplyResult{ProviderCardID: "provider_" + input.CardID})
	})
	mux.HandleFunc("GET /healthz", func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("ok"))
	})
	return mux
}
