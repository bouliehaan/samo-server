CREATE TABLE IF NOT EXISTS shelf_bookmarks (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  position_seconds INTEGER NOT NULL DEFAULT 0,
  chapter_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS shelf_collections (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  public INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS shelf_collection_items (
  collection_id TEXT NOT NULL REFERENCES shelf_collections(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  position INTEGER NOT NULL DEFAULT 0,
  added_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (collection_id, item_id)
);

CREATE TABLE IF NOT EXISTS shelf_listening_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES shelf_items(id) ON DELETE CASCADE,
  started_at TEXT NOT NULL,
  ended_at TEXT NOT NULL,
  start_position_seconds INTEGER NOT NULL DEFAULT 0,
  end_position_seconds INTEGER NOT NULL DEFAULT 0,
  duration_seconds INTEGER NOT NULL DEFAULT 0,
  completed INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_shelf_bookmarks_user_item ON shelf_bookmarks(user_id, item_id);
CREATE INDEX IF NOT EXISTS idx_shelf_collections_user ON shelf_collections(user_id);
CREATE INDEX IF NOT EXISTS idx_shelf_collection_items_collection ON shelf_collection_items(collection_id, position);
CREATE INDEX IF NOT EXISTS idx_shelf_listening_sessions_user_started ON shelf_listening_sessions(user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_shelf_listening_sessions_item ON shelf_listening_sessions(item_id);
