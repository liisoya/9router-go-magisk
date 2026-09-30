package app

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	"go.uber.org/fx"

	"9router/proxy/internal/config"
	"9router/proxy/internal/db"
)

// DatabaseModule handles database initialization and provides *sql.DB and *db.Repo.
var DatabaseModule = fx.Module("database",
	fx.Provide(
		ProvideDatabase,
		ProvideRepo,
	),
)

// ProvideDatabase initializes the global SQLite database and registers an OnStop lifecycle hook to close it cleanly.
func ProvideDatabase(lc fx.Lifecycle, cfg *config.Config) (*sql.DB, error) {
	if err := db.InitGlobalDatabase(cfg.DatabasePath); err != nil {
		return nil, fmt.Errorf("database init: %w", err)
	}

	conn, err := db.GetConnection()
	if err != nil {
		return nil, fmt.Errorf("database connect: %w", err)
	}

	// Upstream core schema bootstrap (fresh .9router): create the shared
	// tables/indexes when absent, backfill missing columns, and seed the
	// minimal rows. Idempotent — existing user data is never touched.
	// Best-effort like leases below: a shared test binary may hand us a
	// connection bound to a removed temp file (global singleton); a dead
	// connection is a test artifact, not a production schema failure.
	if err := db.EnsureCoreSchema(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database schema: %w", err)
		}
	}

	// Cross-process lease table for upstream coordination (Freebuff
	// sessions, future scopes). Idempotent: no-op when already present,
	// invisible to dashboards that do not know the table. Best-effort:
	// a shared test binary may hand us a connection bound to a removed
	// temp file (global singleton); leases then simply stay unavailable.
	if err := db.EnsureUpstreamLeases(conn); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			return nil, fmt.Errorf("database leases: %w", err)
		}
	}

	// 展示前缀自愈：给"有名字但缺 prefix"的节点及其连接补回前缀。
	// 为什么在启动时做：模型列表发布 `openai-compatible-chat-<uuid>/模型名` 这类**内部 ID**
	// 就是因为前缀缺失（2026-09-30 用户反馈），而这类历史数据（旧版本建的节点 / 恢复的备份 /
	// 别的客户端建的节点）不会自己好起来。纯补空（绝不覆盖已有前缀）、单事务、幂等；
	// 读路径另有一份带节流的同款自愈（handlers/chat/prefix_heal.go），这里是"开机即修"。
	// 与 leases 同款 best-effort：共享测试二进制可能拿着已删除临时文件的连接。
	if res, err := db.NewRepo(conn).HealProviderNodePrefixes(); err != nil {
		_, statErr := conn.Exec("SELECT 1")
		if statErr == nil {
			log.Printf("[db] 展示前缀自愈失败（不影响启动）：%v", err)
		}
	} else if res.Nodes > 0 || res.Connections > 0 || res.Resurrected > 0 {
		log.Printf("[db] 展示前缀自愈：补回节点 %d 个 / 连接 %d 个 / 悬空连接补回节点 %d 个",
			res.Nodes, res.Connections, res.Resurrected)
	}

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return conn.Close()
		},
	})

	return conn, nil
}

// ProvideRepo provides *db.Repo using the database connection.
func ProvideRepo(conn *sql.DB) *db.Repo {
	return db.NewRepo(conn)
}
