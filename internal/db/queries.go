package db

import (
	"context"
	"database/sql"
	"errors"
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

const getLastSyncedBlockSQL = `
		SELECT last_block FROM sync_state WHERE id = 1
	`
const updateLastSyncedBlockSQL = `
		UPDATE sync_state SET last_block = $1, updated_at = NOW() WHERE id = 1
	`

type Repo struct {
	db *sqlx.DB
}

func NewRepo(db *sqlx.DB) *Repo {
	return &Repo{db: db}
}

//func toRow(e indexer.Event) EventRow {
//	return EventRow{
//		ProjectID:   int64(e.ProjectID),
//		Name:        e.Name,
//		URL:         e.URL,
//		Submitter:   e.Submitter,
//		TxHash:      e.TxHash,
//		BlockNumber: int64(e.BlockNumber),
//	}
//}

func (r *Repo) InsertEvent(ctx context.Context, row EventRow) error {
	//row := toRow(event)
	result, err := r.db.NamedExecContext(ctx, insertEventSQL, row)
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}

	counts, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get rows affected: %w", err)
	}
	if counts == 0 {
		// 没有插入任何行，说明 tx_hash 已经存在了（根据 ON CONFLICT 的定义）
		log.Printf("event with tx_hash %s already exists", row.TxHash)
	}
	return nil
}

func (r *Repo) GetLastSyncedBlock(ctx context.Context) (uint64, error) {
	var lastSyncedBlock int64
	err := r.db.GetContext(ctx, &lastSyncedBlock, getLastSyncedBlockSQL)
	if errors.Is(err, sql.ErrNoRows) {
		// 把 ErrNoRows 当作"还没同步过"，返回 0
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("get last synced block: %w", err)
	}
	return uint64(lastSyncedBlock), nil
}

func (r *Repo) UpdateLastSyncedBlock(ctx context.Context, block uint64) error {
	_, err := r.db.ExecContext(ctx, updateLastSyncedBlockSQL, int64(block))
	if err != nil {
		return fmt.Errorf("update last synced block: %w", err)
	}
	return nil
}
