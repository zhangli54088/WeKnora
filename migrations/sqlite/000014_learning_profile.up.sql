-- Auditable learning evidence and per-user Wiki state for SQLite deployments.
-- source_id is polymorphic; scoped service cleanup replaces a direct FK.
CREATE TABLE IF NOT EXISTS learning_evidences (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    wiki_page_id VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    evidence_type VARCHAR(64) NOT NULL,
    level VARCHAR(32) NOT NULL,
    source_type VARCHAR(64) NOT NULL,
    source_id VARCHAR(64) NOT NULL,
    weight REAL NOT NULL DEFAULT 1,
    metadata JSON NOT NULL DEFAULT '{}',
    occurred_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_learning_evidence_source
    ON learning_evidences (tenant_id, subject_id, source_type, source_id, wiki_page_id);

CREATE INDEX IF NOT EXISTS idx_learning_evidences_user
    ON learning_evidences (tenant_id, subject_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_learning_evidences_wiki
    ON learning_evidences (tenant_id, wiki_page_id);

CREATE TABLE IF NOT EXISTS user_knowledge_states (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    wiki_page_id VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    status VARCHAR(32) NOT NULL,
    confidence REAL NOT NULL DEFAULT 0,
    evidence_count INTEGER NOT NULL DEFAULT 0,
    last_evidence_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_user_knowledge_state_scope
    ON user_knowledge_states (tenant_id, subject_id, wiki_page_id);

CREATE INDEX IF NOT EXISTS idx_user_knowledge_states_user
    ON user_knowledge_states (tenant_id, subject_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_user_knowledge_states_wiki
    ON user_knowledge_states (tenant_id, wiki_page_id);
