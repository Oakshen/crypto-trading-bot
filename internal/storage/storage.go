package storage

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// TradingSession represents a trading analysis session
// TradingSession 表示一次交易分析会话
type TradingSession struct {
	ID              int64
	BatchID         string // 批次ID - 同一次运行的所有交易对共享相同 batch_id / Batch ID - all symbols in same run share same batch_id
	Symbol          string
	Timeframe       string
	CreatedAt       time.Time
	MarketReport    string
	CryptoReport    string
	SentimentReport string
	PositionInfo    string
	Decision        string // 该交易对的专属决策 / Symbol-specific decision
	FullDecision    string // LLM 原始完整决策（包含所有交易对）/ Full LLM decision (all symbols)
	Executed        bool
	ExecutionResult string
}

// PositionRecord represents an active trading position
// PositionRecord 表示一个活跃的交易持仓
type PositionRecord struct {
	ID               string
	Symbol           string
	Side             string
	EntryPrice       float64
	EntryTime        time.Time
	Quantity         float64
	Leverage         int // 杠杆倍数 / Leverage multiplier
	InitialStopLoss  float64
	CurrentStopLoss  float64
	StopLossType     string
	TrailingDistance float64
	HighestPrice     float64
	CurrentPrice     float64
	UnrealizedPnL    float64
	OpenReason       string
	ATR              float64
	StopLossOrderID  string // 止损单 ID / Stop-loss order ID
	Closed           bool
	CloseTime        *time.Time
	ClosePrice       float64
	CloseReason      string
	RealizedPnL      float64
}

// StopLossEvent represents a stop-loss change event
// StopLossEvent 表示一次止损变更事件
type StopLossEvent struct {
	ID         int64
	PositionID string
	Timestamp  time.Time
	OldStop    float64
	NewStop    float64
	Reason     string
	Trigger    string
}

// BalanceHistory represents account balance at a point in time
// BalanceHistory 表示某个时间点的账户余额
type BalanceHistory struct {
	ID               int64
	Timestamp        time.Time
	TotalBalance     float64
	AvailableBalance float64
	UnrealizedPnL    float64
	Positions        int
}

// BatchSession represents a batch of trading sessions (all symbols from one execution)
// BatchSession 表示一批交易会话（一次运行中所有交易对的会话）
type BatchSession struct {
	BatchID   string
	CreatedAt time.Time
	Timeframe string
	Sessions  []*TradingSession
}

// Storage handles SQLite database operations
type Storage struct {
	db *sql.DB
}

// NewStorage creates a new storage instance
func NewStorage(dbPath string) (*Storage, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	storage := &Storage{db: db}

	// Initialize schema
	if err := storage.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return storage, nil
}

// initSchema creates database tables if they don't exist
// initSchema 创建数据库表（如果不存在）
func (s *Storage) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS trading_sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		batch_id TEXT,
		symbol TEXT NOT NULL,
		timeframe TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		market_report TEXT,
		crypto_report TEXT,
		sentiment_report TEXT,
		position_info TEXT,
		decision TEXT,
		full_decision TEXT,
		leverage INTEGER,
		executed BOOLEAN DEFAULT 0,
		execution_result TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_symbol_created_at ON trading_sessions(symbol, created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_created_at ON trading_sessions(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_batch_id ON trading_sessions(batch_id);

	CREATE TABLE IF NOT EXISTS positions (
		id TEXT PRIMARY KEY,
		symbol TEXT NOT NULL,
		side TEXT NOT NULL,
		entry_price REAL NOT NULL,
		entry_time DATETIME NOT NULL,
		quantity REAL NOT NULL,
		leverage INTEGER NOT NULL DEFAULT 10,
		initial_stop_loss REAL NOT NULL,
		current_stop_loss REAL NOT NULL,
		stop_loss_type TEXT NOT NULL,
		trailing_distance REAL,
		highest_price REAL NOT NULL,
		current_price REAL NOT NULL,
		unrealized_pnl REAL,
		open_reason TEXT,
		atr REAL,
		stop_loss_order_id TEXT,
		closed BOOLEAN DEFAULT 0,
		close_time DATETIME,
		close_price REAL,
		close_reason TEXT,
		realized_pnl REAL
	);

	CREATE INDEX IF NOT EXISTS idx_positions_symbol ON positions(symbol);
	CREATE INDEX IF NOT EXISTS idx_positions_closed ON positions(closed);

	CREATE TABLE IF NOT EXISTS stoploss_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		position_id TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		old_stop REAL NOT NULL,
		new_stop REAL NOT NULL,
		reason TEXT,
		trigger TEXT,
		FOREIGN KEY (position_id) REFERENCES positions(id)
	);

	CREATE INDEX IF NOT EXISTS idx_stoploss_position ON stoploss_events(position_id, timestamp DESC);

	CREATE TABLE IF NOT EXISTS balance_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp DATETIME NOT NULL,
		total_balance REAL NOT NULL,
		available_balance REAL NOT NULL,
		unrealized_pnl REAL DEFAULT 0,
		positions INTEGER DEFAULT 0
	);

	CREATE INDEX IF NOT EXISTS idx_balance_timestamp ON balance_history(timestamp DESC);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return err
	}

	// Migrate existing database: add batch_id and full_decision columns if they don't exist
	// 迁移现有数据库：如果不存在则添加 batch_id 和 full_decision 字段
	migrationSQL := `
	ALTER TABLE trading_sessions ADD COLUMN batch_id TEXT;
	ALTER TABLE trading_sessions ADD COLUMN full_decision TEXT;
	ALTER TABLE positions ADD COLUMN stop_loss_order_id TEXT;
	`
	// Ignore errors as columns may already exist
	// 忽略错误，因为字段可能已经存在
	s.db.Exec(migrationSQL)

	return nil
}

// SaveSession saves a trading session to the database
func (s *Storage) SaveSession(session *TradingSession) (int64, error) {
	query := `
	INSERT INTO trading_sessions (
		batch_id, symbol, timeframe, created_at,
		market_report, crypto_report, sentiment_report,
		position_info, decision, full_decision, executed, execution_result
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := s.db.Exec(
		query,
		session.BatchID,
		session.Symbol,
		session.Timeframe,
		session.CreatedAt,
		session.MarketReport,
		session.CryptoReport,
		session.SentimentReport,
		session.PositionInfo,
		session.Decision,
		session.FullDecision,
		session.Executed,
		session.ExecutionResult,
	)

	if err != nil {
		return 0, fmt.Errorf("failed to save session: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return id, nil
}

// GetLatestSessions retrieves the latest N sessions
func (s *Storage) GetLatestSessions(limit int) ([]*TradingSession, error) {
	query := `
	SELECT id, batch_id, symbol, timeframe, created_at,
		   market_report, crypto_report, sentiment_report,
		   position_info, decision, full_decision, executed, execution_result
	FROM trading_sessions
	ORDER BY created_at DESC
	LIMIT ?
	`

	rows, err := s.db.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*TradingSession
	for rows.Next() {
		session := &TradingSession{}
		err := rows.Scan(
			&session.ID,
			&session.BatchID,
			&session.Symbol,
			&session.Timeframe,
			&session.CreatedAt,
			&session.MarketReport,
			&session.CryptoReport,
			&session.SentimentReport,
			&session.PositionInfo,
			&session.Decision,
			&session.FullDecision,
			&session.Executed,
			&session.ExecutionResult,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

// GetSessionByID retrieves a session by its ID
// GetSessionByID 根据 ID 获取会话
func (s *Storage) GetSessionByID(id int64) (*TradingSession, error) {
	query := `
	SELECT id, batch_id, symbol, timeframe, created_at,
		   market_report, crypto_report, sentiment_report,
		   position_info, decision, full_decision, executed, execution_result
	FROM trading_sessions
	WHERE id = ?
	`

	session := &TradingSession{}
	err := s.db.QueryRow(query, id).Scan(
		&session.ID,
		&session.BatchID,
		&session.Symbol,
		&session.Timeframe,
		&session.CreatedAt,
		&session.MarketReport,
		&session.CryptoReport,
		&session.SentimentReport,
		&session.PositionInfo,
		&session.Decision,
		&session.FullDecision,
		&session.Executed,
		&session.ExecutionResult,
	)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found: %d", id)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query session: %w", err)
	}

	return session, nil
}

// GetLatestBatches retrieves the latest N batches (grouped by batch_id)
// GetLatestBatches 获取最新的 N 个批次（按 batch_id 分组）
func (s *Storage) GetLatestBatches(limit int) ([]*BatchSession, error) {
	// Use a subquery to get one representative session per batch_id
	// 使用子查询获取每个 batch_id 的代表性会话
	batchQuery := `
	SELECT t1.batch_id, t1.created_at, t1.timeframe
	FROM trading_sessions t1
	INNER JOIN (
		SELECT batch_id, MIN(id) as min_id
		FROM trading_sessions
		WHERE batch_id IS NOT NULL AND batch_id != ''
		GROUP BY batch_id
	) t2 ON t1.batch_id = t2.batch_id AND t1.id = t2.min_id
	ORDER BY t1.created_at DESC
	LIMIT ?
	`

	rows, err := s.db.Query(batchQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query batches: %w", err)
	}
	defer rows.Close()

	var batches []*BatchSession
	for rows.Next() {
		batch := &BatchSession{}
		err := rows.Scan(&batch.BatchID, &batch.CreatedAt, &batch.Timeframe)
		if err != nil {
			return nil, fmt.Errorf("failed to scan batch: %w", err)
		}
		batches = append(batches, batch)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// For each batch, get all sessions
	// 对于每个批次，获取所有会话
	sessionQuery := `
	SELECT id, batch_id, symbol, timeframe, created_at,
		   market_report, crypto_report, sentiment_report,
		   position_info, decision, full_decision, executed, execution_result
	FROM trading_sessions
	WHERE batch_id = ?
	ORDER BY symbol
	`

	for _, batch := range batches {
		sessionRows, err := s.db.Query(sessionQuery, batch.BatchID)
		if err != nil {
			return nil, fmt.Errorf("failed to query sessions for batch %s: %w", batch.BatchID, err)
		}

		for sessionRows.Next() {
			session := &TradingSession{}
			err := sessionRows.Scan(
				&session.ID,
				&session.BatchID,
				&session.Symbol,
				&session.Timeframe,
				&session.CreatedAt,
				&session.MarketReport,
				&session.CryptoReport,
				&session.SentimentReport,
				&session.PositionInfo,
				&session.Decision,
				&session.FullDecision,
				&session.Executed,
				&session.ExecutionResult,
			)
			if err != nil {
				sessionRows.Close()
				return nil, fmt.Errorf("failed to scan session: %w", err)
			}
			batch.Sessions = append(batch.Sessions, session)
		}
		sessionRows.Close()

		if err := sessionRows.Err(); err != nil {
			return nil, err
		}
	}

	return batches, nil
}

// GetSessionsBySymbol retrieves sessions for a specific symbol
func (s *Storage) GetSessionsBySymbol(symbol string, limit int) ([]*TradingSession, error) {
	query := `
	SELECT id, batch_id, symbol, timeframe, created_at,
		   market_report, crypto_report, sentiment_report,
		   position_info, decision, full_decision, executed, execution_result
	FROM trading_sessions
	WHERE symbol = ?
	ORDER BY created_at DESC
	LIMIT ?
	`

	rows, err := s.db.Query(query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*TradingSession
	for rows.Next() {
		session := &TradingSession{}
		err := rows.Scan(
			&session.ID,
			&session.BatchID,
			&session.Symbol,
			&session.Timeframe,
			&session.CreatedAt,
			&session.MarketReport,
			&session.CryptoReport,
			&session.SentimentReport,
			&session.PositionInfo,
			&session.Decision,
			&session.FullDecision,
			&session.Executed,
			&session.ExecutionResult,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessions = append(sessions, session)
	}

	return sessions, rows.Err()
}

// GetSessionStats returns statistics about trading sessions
func (s *Storage) GetSessionStats(symbol string) (map[string]interface{}, error) {
	query := `
	SELECT
		COUNT(*) as total_sessions,
		COALESCE(SUM(CASE WHEN executed = 1 THEN 1 ELSE 0 END), 0) as executed_count,
		COALESCE(MIN(created_at), '') as first_session,
		COALESCE(MAX(created_at), '') as last_session
	FROM trading_sessions
	WHERE symbol = ?
	`

	var totalSessions, executedCount int
	var firstSession, lastSession string

	err := s.db.QueryRow(query, symbol).Scan(
		&totalSessions,
		&executedCount,
		&firstSession,
		&lastSession,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	stats := map[string]interface{}{
		"total_sessions": totalSessions,
		"executed_count": executedCount,
		"first_session":  firstSession,
		"last_session":   lastSession,
		"execution_rate": 0.0,
	}

	if totalSessions > 0 {
		stats["execution_rate"] = float64(executedCount) / float64(totalSessions) * 100
	}

	return stats, nil
}

// UpdateExecutionResult updates the execution result for a session
func (s *Storage) UpdateExecutionResult(sessionID int64, executed bool, result string) error {
	query := `
	UPDATE trading_sessions
	SET executed = ?, execution_result = ?
	WHERE id = ?
	`

	_, err := s.db.Exec(query, executed, result, sessionID)
	if err != nil {
		return fmt.Errorf("failed to update execution result: %w", err)
	}

	return nil
}

// UpdateLatestSessionExecution updates the execution result for the latest session of a symbol
// UpdateLatestSessionExecution 更新某个交易对最新会话的执行结果
func (s *Storage) UpdateLatestSessionExecution(symbol string, timeframe string, executed bool, result string) error {
	query := `
	UPDATE trading_sessions
	SET executed = ?, execution_result = ?
	WHERE symbol = ? AND timeframe = ?
	AND id = (
		SELECT id FROM trading_sessions
		WHERE symbol = ? AND timeframe = ?
		ORDER BY created_at DESC
		LIMIT 1
	)
	`

	_, err := s.db.Exec(query, executed, result, symbol, timeframe, symbol, timeframe)
	if err != nil {
		return fmt.Errorf("failed to update latest session execution: %w", err)
	}

	return nil
}

// SaveBalanceHistory saves account balance snapshot to history
// SaveBalanceHistory 保存账户余额快照到历史记录
func (s *Storage) SaveBalanceHistory(balance *BalanceHistory) error {
	query := `
	INSERT INTO balance_history (
		timestamp, total_balance, available_balance, unrealized_pnl, positions
	) VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(
		query,
		balance.Timestamp,
		balance.TotalBalance,
		balance.AvailableBalance,
		balance.UnrealizedPnL,
		balance.Positions,
	)

	if err != nil {
		return fmt.Errorf("failed to save balance history: %w", err)
	}

	return nil
}

// GetBalanceHistory retrieves balance history for the last N hours
// GetBalanceHistory 获取最近 N 小时的余额历史
func (s *Storage) GetBalanceHistory(hours int) ([]*BalanceHistory, error) {
	// SQLite's datetime('now') is UTC, but timestamps are written as local
	// time.Time values, so comparing the two shifted the window by the local
	// UTC offset: on a UTC-7 host the 1h and 3h ranges returned nothing and the
	// dashboard chart came up blank, while 1d silently returned only 17 hours.
	// Computing the cutoff in Go and binding it as a parameter makes the
	// comparison use the exact same representation the writer produced.
	// SQLite 的 datetime('now') 是 UTC 时间，而 timestamp 是以本地时间的
	// time.Time 写入的，二者比较会产生本地时区偏移量大小的窗口错位：
	// 在 UTC-7 的主机上，1h 与 3h 范围查不到任何数据，仪表盘图表一片空白，
	// 而 1d 实际只返回了 17 小时的数据。
	// 在 Go 侧计算截止时间并作为参数绑定，可确保比较双方使用完全相同的表示形式。
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	query := `
	SELECT id, timestamp, total_balance, available_balance, unrealized_pnl, positions
	FROM balance_history
	WHERE timestamp >= ?
	ORDER BY timestamp ASC
	`

	rows, err := s.db.Query(query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("failed to query balance history: %w", err)
	}
	defer rows.Close()

	var history []*BalanceHistory
	for rows.Next() {
		h := &BalanceHistory{}
		err := rows.Scan(
			&h.ID,
			&h.Timestamp,
			&h.TotalBalance,
			&h.AvailableBalance,
			&h.UnrealizedPnL,
			&h.Positions,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan balance history: %w", err)
		}
		history = append(history, h)
	}

	return history, rows.Err()
}

// Close closes the database connection
func (s *Storage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// SavePosition saves a position to the database
// SavePosition 保存持仓到数据库
func (s *Storage) SavePosition(pos *PositionRecord) error {
	query := `
	INSERT INTO positions (
		id, symbol, side, entry_price, entry_time, quantity, leverage,
		initial_stop_loss, current_stop_loss, stop_loss_type,
		trailing_distance, highest_price, current_price,
		unrealized_pnl, open_reason, atr, stop_loss_order_id, closed
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(
		query,
		pos.ID, pos.Symbol, pos.Side, pos.EntryPrice, pos.EntryTime, pos.Quantity, pos.Leverage,
		pos.InitialStopLoss, pos.CurrentStopLoss, pos.StopLossType,
		pos.TrailingDistance, pos.HighestPrice, pos.CurrentPrice,
		pos.UnrealizedPnL, pos.OpenReason, pos.ATR, pos.StopLossOrderID, pos.Closed,
	)

	if err != nil {
		return fmt.Errorf("failed to save position: %w", err)
	}

	return nil
}

// UpdatePosition updates a position in the database
// UpdatePosition 更新持仓信息
func (s *Storage) UpdatePosition(pos *PositionRecord) error {
	query := `
	UPDATE positions SET
		current_stop_loss = ?,
		stop_loss_type = ?,
		trailing_distance = ?,
		highest_price = ?,
		current_price = ?,
		unrealized_pnl = ?,
		stop_loss_order_id = ?,
		closed = ?,
		close_time = ?,
		close_price = ?,
		close_reason = ?,
		realized_pnl = ?
	WHERE id = ?
	`

	_, err := s.db.Exec(
		query,
		pos.CurrentStopLoss, pos.StopLossType, pos.TrailingDistance,
		pos.HighestPrice, pos.CurrentPrice, pos.UnrealizedPnL,
		pos.StopLossOrderID,
		pos.Closed, pos.CloseTime, pos.ClosePrice, pos.CloseReason, pos.RealizedPnL,
		pos.ID,
	)

	if err != nil {
		return fmt.Errorf("failed to update position: %w", err)
	}

	return nil
}

// GetActivePositions retrieves all active (non-closed) positions
// GetActivePositions 获取所有活跃持仓
func (s *Storage) GetActivePositions() ([]*PositionRecord, error) {
	query := `
	SELECT id, symbol, side, entry_price, entry_time, quantity, leverage,
		   initial_stop_loss, current_stop_loss, stop_loss_type,
		   trailing_distance, highest_price, current_price,
		   unrealized_pnl, open_reason, atr, stop_loss_order_id, closed,
		   close_time, close_price, close_reason, realized_pnl
	FROM positions
	WHERE closed = 0
	ORDER BY entry_time DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query positions: %w", err)
	}
	defer rows.Close()

	var positions []*PositionRecord
	for rows.Next() {
		pos := &PositionRecord{}
		var trailingDistance, unrealizedPnL, atr, closePrice, realizedPnL sql.NullFloat64
		var closeTime sql.NullTime
		var closeReason, stopLossOrderID sql.NullString

		err := rows.Scan(
			&pos.ID, &pos.Symbol, &pos.Side, &pos.EntryPrice, &pos.EntryTime, &pos.Quantity, &pos.Leverage,
			&pos.InitialStopLoss, &pos.CurrentStopLoss, &pos.StopLossType,
			&trailingDistance, &pos.HighestPrice, &pos.CurrentPrice,
			&unrealizedPnL, &pos.OpenReason, &atr, &stopLossOrderID, &pos.Closed,
			&closeTime, &closePrice, &closeReason, &realizedPnL,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}

		// Handle NULL values
		// 处理 NULL 值
		if trailingDistance.Valid {
			pos.TrailingDistance = trailingDistance.Float64
		}
		if unrealizedPnL.Valid {
			pos.UnrealizedPnL = unrealizedPnL.Float64
		}
		if atr.Valid {
			pos.ATR = atr.Float64
		}
		if stopLossOrderID.Valid {
			pos.StopLossOrderID = stopLossOrderID.String
		}
		if closeTime.Valid {
			pos.CloseTime = &closeTime.Time
		}
		if closePrice.Valid {
			pos.ClosePrice = closePrice.Float64
		}
		if closeReason.Valid {
			pos.CloseReason = closeReason.String
		}
		if realizedPnL.Valid {
			pos.RealizedPnL = realizedPnL.Float64
		}

		positions = append(positions, pos)
	}

	return positions, rows.Err()
}

// GetPositionsBySymbol retrieves positions for a specific symbol
// GetPositionsBySymbol 获取特定交易对的持仓
func (s *Storage) GetPositionsBySymbol(symbol string) ([]*PositionRecord, error) {
	query := `
	SELECT id, symbol, side, entry_price, entry_time, quantity, leverage,
		   initial_stop_loss, current_stop_loss, stop_loss_type,
		   trailing_distance, highest_price, current_price,
		   unrealized_pnl, open_reason, atr, stop_loss_order_id, closed,
		   close_time, close_price, close_reason, realized_pnl
	FROM positions
	WHERE symbol = ?
	ORDER BY entry_time DESC
	LIMIT 20
	`

	rows, err := s.db.Query(query, symbol)
	if err != nil {
		return nil, fmt.Errorf("failed to query positions: %w", err)
	}
	defer rows.Close()

	var positions []*PositionRecord
	for rows.Next() {
		pos := &PositionRecord{}
		var trailingDistance, unrealizedPnL, atr, closePrice, realizedPnL sql.NullFloat64
		var closeTime sql.NullTime
		var closeReason, stopLossOrderID sql.NullString

		err := rows.Scan(
			&pos.ID, &pos.Symbol, &pos.Side, &pos.EntryPrice, &pos.EntryTime, &pos.Quantity, &pos.Leverage,
			&pos.InitialStopLoss, &pos.CurrentStopLoss, &pos.StopLossType,
			&trailingDistance, &pos.HighestPrice, &pos.CurrentPrice,
			&unrealizedPnL, &pos.OpenReason, &atr, &stopLossOrderID, &pos.Closed,
			&closeTime, &closePrice, &closeReason, &realizedPnL,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan position: %w", err)
		}

		// Handle NULL values
		// 处理 NULL 值
		if trailingDistance.Valid {
			pos.TrailingDistance = trailingDistance.Float64
		}
		if unrealizedPnL.Valid {
			pos.UnrealizedPnL = unrealizedPnL.Float64
		}
		if atr.Valid {
			pos.ATR = atr.Float64
		}
		if stopLossOrderID.Valid {
			pos.StopLossOrderID = stopLossOrderID.String
		}
		if closeTime.Valid {
			pos.CloseTime = &closeTime.Time
		}
		if closePrice.Valid {
			pos.ClosePrice = closePrice.Float64
		}
		if closeReason.Valid {
			pos.CloseReason = closeReason.String
		}
		if realizedPnL.Valid {
			pos.RealizedPnL = realizedPnL.Float64
		}

		positions = append(positions, pos)
	}

	return positions, rows.Err()
}

// GetPositionByID retrieves a single position by its ID
// GetPositionByID 根据 ID 获取单个持仓
func (s *Storage) GetPositionByID(positionID string) (*PositionRecord, error) {
	query := `
	SELECT id, symbol, side, entry_price, entry_time, quantity, leverage,
		   initial_stop_loss, current_stop_loss, stop_loss_type,
		   trailing_distance, highest_price, current_price,
		   unrealized_pnl, open_reason, atr, stop_loss_order_id, closed,
		   close_time, close_price, close_reason, realized_pnl
	FROM positions
	WHERE id = ?
	LIMIT 1
	`

	row := s.db.QueryRow(query, positionID)

	pos := &PositionRecord{}
	var trailingDistance, unrealizedPnL, atr, closePrice, realizedPnL sql.NullFloat64
	var closeTime sql.NullTime
	var closeReason, stopLossOrderID sql.NullString

	err := row.Scan(
		&pos.ID, &pos.Symbol, &pos.Side, &pos.EntryPrice, &pos.EntryTime, &pos.Quantity, &pos.Leverage,
		&pos.InitialStopLoss, &pos.CurrentStopLoss, &pos.StopLossType,
		&trailingDistance, &pos.HighestPrice, &pos.CurrentPrice,
		&unrealizedPnL, &pos.OpenReason, &atr, &stopLossOrderID, &pos.Closed,
		&closeTime, &closePrice, &closeReason, &realizedPnL,
	)

	if err == sql.ErrNoRows {
		return nil, nil // No position found / 未找到持仓
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get position: %w", err)
	}

	// Handle NULL values
	// 处理 NULL 值
	if trailingDistance.Valid {
		pos.TrailingDistance = trailingDistance.Float64
	}
	if unrealizedPnL.Valid {
		pos.UnrealizedPnL = unrealizedPnL.Float64
	}
	if atr.Valid {
		pos.ATR = atr.Float64
	}
	if stopLossOrderID.Valid {
		pos.StopLossOrderID = stopLossOrderID.String
	}
	if closeTime.Valid {
		pos.CloseTime = &closeTime.Time
	}
	if closePrice.Valid {
		pos.ClosePrice = closePrice.Float64
	}
	if closeReason.Valid {
		pos.CloseReason = closeReason.String
	}
	if realizedPnL.Valid {
		pos.RealizedPnL = realizedPnL.Float64
	}

	return pos, nil
}

// SaveStopLossEvent saves a stop-loss event to the database
// SaveStopLossEvent 保存止损事件到数据库
func (s *Storage) SaveStopLossEvent(event *StopLossEvent) error {
	query := `
	INSERT INTO stoploss_events (
		position_id, timestamp, old_stop, new_stop, reason, trigger
	) VALUES (?, ?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(
		query,
		event.PositionID, event.Timestamp, event.OldStop,
		event.NewStop, event.Reason, event.Trigger,
	)

	if err != nil {
		return fmt.Errorf("failed to save stop-loss event: %w", err)
	}

	return nil
}

// GetStopLossEvents retrieves stop-loss events for a position
// GetStopLossEvents 获取持仓的止损事件历史
func (s *Storage) GetStopLossEvents(positionID string) ([]*StopLossEvent, error) {
	query := `
	SELECT id, position_id, timestamp, old_stop, new_stop, reason, trigger
	FROM stoploss_events
	WHERE position_id = ?
	ORDER BY timestamp ASC
	`

	rows, err := s.db.Query(query, positionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query stop-loss events: %w", err)
	}
	defer rows.Close()

	var events []*StopLossEvent
	for rows.Next() {
		event := &StopLossEvent{}
		err := rows.Scan(
			&event.ID, &event.PositionID, &event.Timestamp,
			&event.OldStop, &event.NewStop, &event.Reason, &event.Trigger,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan stop-loss event: %w", err)
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

// GetTotalSessionCount retrieves the total number of trading sessions
// GetTotalSessionCount 获取交易会话总数
func (s *Storage) GetTotalSessionCount() (int, error) {
	var count int
	query := "SELECT COUNT(*) FROM trading_sessions"
	err := s.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count sessions: %w", err)
	}
	return count, nil
}

// GetTotalBatchCount retrieves the total number of unique batches
// GetTotalBatchCount 获取唯一批次总数
func (s *Storage) GetTotalBatchCount() (int, error) {
	var count int
	query := "SELECT COUNT(DISTINCT batch_id) FROM trading_sessions"
	err := s.db.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count batches: %w", err)
	}
	return count, nil
}

// GetBatchesWithPagination retrieves batches with pagination support
// GetBatchesWithPagination 支持分页的批次获取
func (s *Storage) GetBatchesWithPagination(offset, limit int) ([]*BatchSession, error) {
	// Get unique batch_ids with pagination
	// 获取唯一的 batch_id 并分页
	batchQuery := `
	SELECT DISTINCT t1.batch_id, t1.created_at, t1.timeframe
	FROM trading_sessions t1
	INNER JOIN (
		SELECT batch_id, MIN(id) as min_id
		FROM trading_sessions
		GROUP BY batch_id
	) t2 ON t1.batch_id = t2.batch_id AND t1.id = t2.min_id
	ORDER BY t1.created_at DESC
	LIMIT ? OFFSET ?
	`

	batchRows, err := s.db.Query(batchQuery, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query batches: %w", err)
	}
	defer batchRows.Close()

	var batches []*BatchSession
	var batchIDs []string

	// Collect batch metadata
	// 收集批次元数据
	for batchRows.Next() {
		batch := &BatchSession{}
		err := batchRows.Scan(&batch.BatchID, &batch.CreatedAt, &batch.Timeframe)
		if err != nil {
			return nil, fmt.Errorf("failed to scan batch: %w", err)
		}
		batches = append(batches, batch)
		batchIDs = append(batchIDs, batch.BatchID)
	}

	if err := batchRows.Err(); err != nil {
		return nil, err
	}

	// If no batches found, return empty result
	// 如果没有找到批次，返回空结果
	if len(batchIDs) == 0 {
		return batches, nil
	}

	// Build placeholders for IN clause
	// 构建 IN 子句的占位符
	placeholders := ""
	for i := range batchIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
	}

	// Get all sessions for these batches
	// 获取这些批次的所有会话
	sessionsQuery := fmt.Sprintf(`
	SELECT id, batch_id, symbol, timeframe, created_at,
		   market_report, crypto_report, sentiment_report,
		   position_info, decision, full_decision, executed, execution_result
	FROM trading_sessions
	WHERE batch_id IN (%s)
	ORDER BY batch_id, symbol
	`, placeholders)

	// Convert batchIDs to []interface{} for variadic args
	// 将 batchIDs 转换为 []interface{} 用于可变参数
	args := make([]interface{}, len(batchIDs))
	for i, id := range batchIDs {
		args[i] = id
	}

	sessionRows, err := s.db.Query(sessionsQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	defer sessionRows.Close()

	// Group sessions by batch_id
	// 按 batch_id 分组会话
	sessionsByBatch := make(map[string][]*TradingSession)
	for sessionRows.Next() {
		session := &TradingSession{}
		err := sessionRows.Scan(
			&session.ID,
			&session.BatchID,
			&session.Symbol,
			&session.Timeframe,
			&session.CreatedAt,
			&session.MarketReport,
			&session.CryptoReport,
			&session.SentimentReport,
			&session.PositionInfo,
			&session.Decision,
			&session.FullDecision,
			&session.Executed,
			&session.ExecutionResult,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan session: %w", err)
		}
		sessionsByBatch[session.BatchID] = append(sessionsByBatch[session.BatchID], session)
	}

	if err := sessionRows.Err(); err != nil {
		return nil, err
	}

	// Assign sessions to batches
	// 将会话分配给批次
	for _, batch := range batches {
		batch.Sessions = sessionsByBatch[batch.BatchID]
	}

	return batches, nil
}

// PerformanceStats summarises how the bot has actually traded.
// PerformanceStats 汇总机器人的实际交易表现
//
// The project's thesis is that returns come from selectivity and a high
// win/loss ratio rather than a high win rate, so those are the figures the
// dashboard leads with.
// 本项目的核心观点是：收益来自极致的选择性与高盈亏比，而非高胜率，
// 因此仪表盘优先展示这几个指标。
type PerformanceStats struct {
	// Decision selectivity / 决策选择性
	TotalDecisions int     // 决策总数 / Total decisions made
	TradedCount    int     // 实际下单次数 / Decisions acted on
	SelectivityPct float64 // 出手率 / Share of decisions acted on

	// Realised outcomes over closed positions / 已平仓的实际结果
	ClosedCount   int     // 已平仓数 / Closed positions
	WinCount      int     // 盈利次数 / Winning trades
	LossCount     int     // 亏损次数 / Losing trades
	WinRatePct    float64 // 胜率 / Win rate
	AvgWin        float64 // 平均盈利 / Average winning trade
	AvgLoss       float64 // 平均亏损（正数）/ Average losing trade (positive)
	WinLossRatio  float64 // 盈亏比 / Average win divided by average loss
	RealisedPnL   float64 // 已实现总盈亏 / Total realised PnL
	BestTrade     float64 // 最佳单笔 / Best single trade
	WorstTrade    float64 // 最差单笔 / Worst single trade
	HasClosedData bool    // 是否有已平仓数据 / Whether any closed trade exists
}

// GetPerformanceStats computes selectivity and realised-outcome statistics.
// GetPerformanceStats 计算选择性与已实现结果的统计数据
func (s *Storage) GetPerformanceStats() (*PerformanceStats, error) {
	stats := &PerformanceStats{}

	// Selectivity comes from the decision log, not the position table: a HOLD
	// never creates a position, so counting positions would hide exactly the
	// behaviour we want to show.
	// 选择性来自决策记录而非持仓表：HOLD 不会产生持仓，
	// 若以持仓计数，恰恰会隐藏我们想展示的行为。
	err := s.db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN executed THEN 1 ELSE 0 END), 0)
		FROM trading_sessions
	`).Scan(&stats.TotalDecisions, &stats.TradedCount)
	if err != nil {
		return nil, fmt.Errorf("failed to query decision counts: %w", err)
	}
	if stats.TotalDecisions > 0 {
		stats.SelectivityPct = float64(stats.TradedCount) / float64(stats.TotalDecisions) * 100
	}

	rows, err := s.db.Query(`
		SELECT COALESCE(realized_pnl, 0)
		FROM positions
		WHERE closed = 1 AND realized_pnl IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query closed positions: %w", err)
	}
	defer rows.Close()

	var totalWin, totalLoss float64
	first := true
	for rows.Next() {
		var pnl float64
		if err := rows.Scan(&pnl); err != nil {
			return nil, fmt.Errorf("failed to scan realised pnl: %w", err)
		}

		stats.ClosedCount++
		stats.RealisedPnL += pnl

		if first {
			stats.BestTrade, stats.WorstTrade, first = pnl, pnl, false
		}
		if pnl > stats.BestTrade {
			stats.BestTrade = pnl
		}
		if pnl < stats.WorstTrade {
			stats.WorstTrade = pnl
		}

		// Break-even counts as neither a win nor a loss, so it does not
		// flatter the win rate.
		// 打平既不算盈利也不算亏损，避免虚增胜率。
		if pnl > 0 {
			stats.WinCount++
			totalWin += pnl
		} else if pnl < 0 {
			stats.LossCount++
			totalLoss += -pnl
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	stats.HasClosedData = stats.ClosedCount > 0
	if stats.ClosedCount > 0 {
		stats.WinRatePct = float64(stats.WinCount) / float64(stats.ClosedCount) * 100
	}
	if stats.WinCount > 0 {
		stats.AvgWin = totalWin / float64(stats.WinCount)
	}
	if stats.LossCount > 0 {
		stats.AvgLoss = totalLoss / float64(stats.LossCount)
	}
	if stats.AvgLoss > 0 {
		stats.WinLossRatio = stats.AvgWin / stats.AvgLoss
	}

	return stats, nil
}

// GetInitialBalance returns the earliest recorded total balance.
// GetInitialBalance 返回最早记录的总余额
//
// This is the baseline the equity curve and the "since start" figure are drawn
// against. Deriving it from the first snapshot means it works on existing
// databases without a migration or a new config value.
// 这是资产曲线与「自启动以来」数值的基准线。
// 从第一条快照推导，可在不迁移数据库、不新增配置的情况下适用于现有数据。
func (s *Storage) GetInitialBalance() (float64, time.Time, error) {
	var balance float64
	var ts time.Time

	err := s.db.QueryRow(`
		SELECT total_balance, timestamp
		FROM balance_history
		ORDER BY timestamp ASC
		LIMIT 1
	`).Scan(&balance, &ts)

	if err == sql.ErrNoRows {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("failed to query initial balance: %w", err)
	}

	return balance, ts, nil
}
