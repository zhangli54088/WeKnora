-- Confirmed MemoryItem -> WikiPage projections for SQLite deployments.
CREATE TABLE IF NOT EXISTS memory_wiki_links (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    subject_id VARCHAR(512) NOT NULL,
    memory_item_id VARCHAR(36) NOT NULL REFERENCES memory_items(id) ON DELETE CASCADE,
    wiki_page_id VARCHAR(36) NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    knowledge_base_id VARCHAR(36) NOT NULL,
    score REAL NOT NULL DEFAULT 0,
    method VARCHAR(64) NOT NULL DEFAULT 'manual',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memory_wiki_link_scope
    ON memory_wiki_links (tenant_id, subject_id, memory_item_id, wiki_page_id);

CREATE INDEX IF NOT EXISTS idx_memory_wiki_links_user
    ON memory_wiki_links (tenant_id, subject_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_memory_wiki_links_kb
    ON memory_wiki_links (tenant_id, knowledge_base_id);
