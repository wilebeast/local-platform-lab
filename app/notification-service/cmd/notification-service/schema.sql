CREATE TABLE IF NOT EXISTS mail_layouts (
    locale VARCHAR(12) PRIMARY KEY,
    shell_html TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mail_templates (
    template_key VARCHAR(100) NOT NULL,
    locale VARCHAR(12) NOT NULL REFERENCES mail_layouts(locale),
    subject_pattern TEXT NOT NULL,
    body_html TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (template_key, locale)
);

CREATE TABLE IF NOT EXISTS mail_delivery_receipts (
    id BIGSERIAL PRIMARY KEY,
    message_id VARCHAR(160) NOT NULL UNIQUE,
    template_key VARCHAR(100) NOT NULL,
    recipient_fingerprint CHAR(64) NOT NULL,
    delivery_state VARCHAR(24) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO mail_layouts(locale, shell_html) VALUES
('en', '<!doctype html><html><body style="font-family:Arial,sans-serif;background:#f4f6f8;padding:24px"><main style="max-width:560px;margin:auto;background:white;padding:28px;border-radius:12px">{{content}}<hr><p style="color:#667085;font-size:12px">Local Platform Lab · Development email</p></main></body></html>'),
('zh-CN', '<!doctype html><html><body style="font-family:Arial,sans-serif;background:#f4f6f8;padding:24px"><main style="max-width:560px;margin:auto;background:white;padding:28px;border-radius:12px">{{content}}<hr><p style="color:#667085;font-size:12px">本地平台实验室 · 开发环境邮件</p></main></body></html>')
ON CONFLICT (locale) DO NOTHING;

INSERT INTO mail_templates(template_key, locale, subject_pattern, body_html) VALUES
('workspace_welcome', 'en', 'Welcome, {{.DisplayName}}', '<h1>Your workspace is ready</h1><p>Hello {{.DisplayName}}, your local workspace has been created.</p>'),
('workspace_welcome', 'zh-CN', '欢迎，{{.DisplayName}}', '<h1>你的工作空间已准备好</h1><p>{{.DisplayName}}，你的本地工作空间已经创建。</p>'),
('email_verification', 'en', 'Your verification code', '<h1>{{.Code}}</h1><p>Use this one-time code to complete {{.Purpose}}. It expires in 10 minutes.</p>'),
('email_verification', 'zh-CN', '你的验证码', '<h1>{{.Code}}</h1><p>请使用该一次性验证码完成 {{.Purpose}}。验证码将在 10 分钟后过期。</p>')
ON CONFLICT (template_key, locale) DO NOTHING;
