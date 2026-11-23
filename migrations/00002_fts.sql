-- +goose Up

CREATE VIRTUAL TABLE messages_fts USING fts5(
    subject,
    from_text,
    to_text,
    body,
    attachments,
    content='',
    contentless_delete=1,
    tokenize='unicode61 remove_diacritics 2',
    prefix='2 3'
);

-- +goose StatementBegin
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
    DELETE FROM messages_fts WHERE rowid = old.pk;
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER IF EXISTS messages_fts_delete;
DROP TABLE IF EXISTS messages_fts;
