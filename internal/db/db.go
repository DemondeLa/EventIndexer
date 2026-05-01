package db

import (
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

func DefaultConfig() Config {
	return Config{
		Host:     "localhost",
		Port:     5432,
		User:     "dev",
		Password: "dev",
		DBName:   "event_indexer",
		SSLMode:  "disable",
	}
}

func (c Config) DSN() string {
	return fmt.Sprintf(
		// postgres://{user}:{password}@{host}:{port}/{dbname}?sslmode={sslmode}
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		c.User, c.Password, c.Host, c.Port, c.DBName, c.SSLMode,
	)
}

func Connect(cfg Config) (*sqlx.DB, error) {
	db, err := sqlx.Connect("postgres", cfg.DSN())
	if err != nil {
		// 连接失败，返回一个包装了原始错误的错误对象，提供更多上下文信息
		return nil, fmt.Errorf("connect to postgres: %w", err)
		// %w：返回的新error包装原err，形成一条错误链
	}
	return db, nil
}

type EventRow struct {
	ProjectID   int64     `db:"project_id"`
	Name        string    `db:"name"`
	URL         string    `db:"url"`
	Submitter   string    `db:"submitter"`
	TxHash      string    `db:"tx_hash"`
	BlockNumber int64     `db:"block_number"`
	IndexedAt   time.Time `db:"indexed_at"`
}
