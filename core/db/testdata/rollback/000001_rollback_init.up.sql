-- The rollback tests work with their own tables; they do not touch alpha/beta state.
CREATE TABLE rollback_items (
    id TEXT PRIMARY KEY
);
