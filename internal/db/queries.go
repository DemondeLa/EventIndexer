package db

import (
	"EventIndexer/internal/indexer"
	"context"
	"fmt"
	"log"

	"github.com/jmoiron/sqlx"
)

const insertEventSQL = `
		INSERT INTO events (
    		project_id, name, url, submitter, tx_hash, block_number
		) VALUES (
		    :project_id, :name, :url, :submitter, :tx_hash, :block_number
		)
		ON CONFLICT (tx_hash) DO NOTHING
	`

type Repo struct {
	db *sqlx.DB
}

func NewRepo(db *sqlx.DB) *Repo {
	return &Repo{db: db}
}

func (repo *Repo) InsertEvent(ctx context.Context, event indexer.Event) error {
	row := toRow(event)
	result, err := repo.db.NamedExecContext(ctx, insertEventSQL, row)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	counts, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if counts == 0 {
		// 没有插入任何行，说明 tx_hash 已经存在了（根据 ON CONFLICT 的定义）
		log.Printf("event with tx_hash %s already exists", event.TxHash)
	}

	return nil
}

func toRow(e indexer.Event) EventRow {
	return EventRow{
		ProjectID:   int64(e.ProjectID),
		Name:        e.Name,
		URL:         e.URL,
		Submitter:   e.Submitter,
		TxHash:      e.TxHash,
		BlockNumber: int64(e.BlockNumber),
	}
}
