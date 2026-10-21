CREATE TABLE IF NOT EXISTS rbac_members (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    UNIQUE (organization_id, user_id)
);

CREATE TABLE IF NOT EXISTS rbac_roles (
    id TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    UNIQUE (organization_id, name)
);

CREATE TABLE IF NOT EXISTS rbac_permissions (
    permission_key TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS rbac_member_roles (
    member_id TEXT NOT NULL REFERENCES rbac_members(id) ON DELETE CASCADE,
    role_id TEXT NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    PRIMARY KEY (member_id, role_id)
);

CREATE TABLE IF NOT EXISTS rbac_role_permissions (
    role_id TEXT NOT NULL REFERENCES rbac_roles(id) ON DELETE CASCADE,
    permission_key TEXT NOT NULL REFERENCES rbac_permissions(permission_key) ON DELETE CASCADE,
    PRIMARY KEY (role_id, permission_key)
);

INSERT INTO rbac_members (id, organization_id, user_id, status)
VALUES ('member-local-user', 'local-org', 'local-user', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO rbac_roles (id, organization_id, name, status)
VALUES ('role-member', 'local-org', 'member', 'active')
ON CONFLICT (id) DO NOTHING;

INSERT INTO rbac_permissions (permission_key, description)
VALUES ('profile.read', 'Read the authenticated user profile')
ON CONFLICT (permission_key) DO NOTHING;

INSERT INTO rbac_member_roles (member_id, role_id)
VALUES ('member-local-user', 'role-member')
ON CONFLICT DO NOTHING;

INSERT INTO rbac_role_permissions (role_id, permission_key)
VALUES ('role-member', 'profile.read')
ON CONFLICT DO NOTHING;
