package notification

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresTemplateRepository struct{ pool *pgxpool.Pool }

func NewPostgresTemplateRepository(pool *pgxpool.Pool) *PostgresTemplateRepository {
	return &PostgresTemplateRepository{pool: pool}
}

func (r *PostgresTemplateRepository) Find(ctx context.Context, key, locale string) (Template, error) {
	var subject, body, shell string
	err := r.pool.QueryRow(ctx, `
		SELECT t.subject_pattern, t.body_html, l.shell_html
		FROM mail_templates t
		JOIN mail_layouts l ON l.locale = t.locale
		WHERE t.template_key = $1 AND t.locale = $2 AND t.enabled = TRUE`, key, locale).
		Scan(&subject, &body, &shell)
	if err != nil {
		return Template{}, err
	}
	if !strings.Contains(shell, "{{content}}") {
		return Template{}, errors.New("mail layout is missing {{content}} marker")
	}
	return Template{SubjectTemplate: subject, HTMLTemplate: strings.Replace(shell, "{{content}}", body, 1)}, nil
}
