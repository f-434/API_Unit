package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"apiunit/internal/model"
	"apiunit/internal/secret"
)

type Store struct {
	db  *sql.DB
	box *secret.Box
}

func Open(path string, box *secret.Box) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(time.Hour)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, box: box}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS targets (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			base_url TEXT NOT NULL,
			protocol TEXT NOT NULL DEFAULT 'auto',
			api_key_enc TEXT NOT NULL DEFAULT '',
			model_count INTEGER NOT NULL DEFAULT 0,
			last_tested_at INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS prompts (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			builtin INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS attacks (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			category TEXT NOT NULL,
			severity TEXT NOT NULL,
			system_prompt TEXT NOT NULL DEFAULT '',
			prompt TEXT NOT NULL,
			canary TEXT NOT NULL DEFAULT '',
			checks_json TEXT NOT NULL DEFAULT '[]',
			require_judge INTEGER NOT NULL DEFAULT 0,
			rubric TEXT NOT NULL DEFAULT '',
			builtin INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS prices (
			id TEXT PRIMARY KEY,
			pattern TEXT NOT NULL,
			input_per_m REAL NOT NULL DEFAULT 0,
			output_per_m REAL NOT NULL DEFAULT 0,
			cache_read_per_m REAL NOT NULL DEFAULT 0,
			cache_write_per_m REAL NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'USD',
			note TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '{}',
			summary_json TEXT NOT NULL DEFAULT '{}',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			started_at INTEGER NOT NULL DEFAULT 0,
			finished_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_kind ON runs(kind, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS run_items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			item_index INTEGER NOT NULL DEFAULT 0,
			attempts INTEGER NOT NULL DEFAULT 0,
			target_id TEXT NOT NULL DEFAULT '',
			target_name TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			protocol TEXT NOT NULL DEFAULT '',
			case_id TEXT NOT NULL DEFAULT '',
			case_name TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			severity TEXT NOT NULL DEFAULT '',
			repetition INTEGER NOT NULL DEFAULT 0,
			started_at INTEGER NOT NULL DEFAULT 0,
			start_offset_ms REAL NOT NULL DEFAULT 0,
			latency_ms REAL NOT NULL DEFAULT 0,
			ttft_ms REAL NOT NULL DEFAULT 0,
			generation_ms REAL NOT NULL DEFAULT 0,
			tpot_ms REAL NOT NULL DEFAULT 0,
			status INTEGER NOT NULL DEFAULT 0,
			ok INTEGER NOT NULL DEFAULT 0,
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			cache_write_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			local_prompt_tokens INTEGER NOT NULL DEFAULT 0,
			token_source TEXT NOT NULL DEFAULT '',
			cost REAL NOT NULL DEFAULT 0,
			error_type TEXT NOT NULL DEFAULT '',
			error_message TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			finish_reason TEXT NOT NULL DEFAULT '',
			output_preview TEXT NOT NULL DEFAULT '',
			output_text TEXT NOT NULL DEFAULT '',
			request_json TEXT NOT NULL DEFAULT '',
			response_json TEXT NOT NULL DEFAULT '',
			verdict TEXT NOT NULL DEFAULT '',
			judge_json TEXT NOT NULL DEFAULT '',
			checks_json TEXT NOT NULL DEFAULT '[]',
			meta_json TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_items_run ON run_items(run_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_items_model ON run_items(run_id, model)`,
		`CREATE TABLE IF NOT EXISTS request_snapshots (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			run_kind TEXT NOT NULL DEFAULT '',
			item_index INTEGER NOT NULL DEFAULT 0,
			case_id TEXT NOT NULL DEFAULT '',
			case_name TEXT NOT NULL DEFAULT '',
			target_id TEXT NOT NULL DEFAULT '',
			target_name TEXT NOT NULL DEFAULT '',
			target_label TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			protocol TEXT NOT NULL DEFAULT '',
			endpoint TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			repetition INTEGER NOT NULL DEFAULT 0,
			request_time_ms INTEGER NOT NULL DEFAULT 0,
			body_sha256 TEXT NOT NULL DEFAULT '',
			body_size INTEGER NOT NULL DEFAULT 0,
			body_text TEXT NOT NULL DEFAULT '',
			canonical_text TEXT NOT NULL DEFAULT '',
			fingerprints_json TEXT NOT NULL DEFAULT '{}',
			findings_json TEXT NOT NULL DEFAULT '[]',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_run ON request_snapshots(run_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_case ON request_snapshots(run_id, case_id, target_id)`,
		`CREATE INDEX IF NOT EXISTS idx_snapshots_time ON request_snapshots(request_time_ms DESC)`,
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range stmts {
		if _, err := tx.Exec(q); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return tx.Commit()
}

// ---------- targets ----------

func (s *Store) ListTargets() ([]model.Target, error) {
	rows, err := s.db.Query(`SELECT id,name,base_url,protocol,api_key_enc,model_count,last_tested_at,created_at,updated_at FROM targets ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Target, 0)
	for rows.Next() {
		var t model.Target
		var enc string
		if err := rows.Scan(&t.ID, &t.Name, &t.BaseURL, &t.Protocol, &enc, &t.ModelCount, &t.LastTestedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		t.HasKey = enc != ""
		if t.HasKey {
			key, err := s.box.Decrypt(enc)
			if err == nil {
				t.KeyMasked = model.MaskKey(key)
			}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTarget(id string, withSecret bool) (*model.TargetSecret, error) {
	var t model.TargetSecret
	var enc string
	err := s.db.QueryRow(`SELECT id,name,base_url,protocol,api_key_enc,model_count,last_tested_at,created_at,updated_at FROM targets WHERE id=?`, id).
		Scan(&t.ID, &t.Name, &t.BaseURL, &t.Protocol, &enc, &t.ModelCount, &t.LastTestedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	t.HasKey = enc != ""
	if withSecret && enc != "" {
		key, err := s.box.Decrypt(enc)
		if err != nil {
			return nil, fmt.Errorf("decrypt target key: %w", err)
		}
		t.APIKey = key
	}
	if t.HasKey {
		if key, err := s.box.Decrypt(enc); err == nil {
			t.KeyMasked = model.MaskKey(key)
		}
	}
	return &t, nil
}

func (s *Store) SaveTarget(t *model.Target, apiKey string) error {
	now := model.NowMs()
	if t.ID == "" {
		t.ID = model.NewID("t_")
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	enc := ""
	if strings.TrimSpace(apiKey) != "" {
		var err error
		enc, err = s.box.Encrypt(apiKey)
		if err != nil {
			return err
		}
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	if enc == "" {
		_, err := s.db.Exec(`INSERT INTO targets(id,name,base_url,protocol,api_key_enc,model_count,last_tested_at,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url, protocol=excluded.protocol,
			model_count=excluded.model_count, last_tested_at=excluded.last_tested_at, updated_at=excluded.updated_at`,
			t.ID, t.Name, t.BaseURL, t.Protocol, "", t.ModelCount, t.LastTestedAt, t.CreatedAt, t.UpdatedAt)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO targets(id,name,base_url,protocol,api_key_enc,model_count,last_tested_at,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url, protocol=excluded.protocol,
		api_key_enc=excluded.api_key_enc, model_count=excluded.model_count, last_tested_at=excluded.last_tested_at, updated_at=excluded.updated_at`,
		t.ID, t.Name, t.BaseURL, t.Protocol, enc, t.ModelCount, t.LastTestedAt, t.CreatedAt, t.UpdatedAt)
	return err
}

func (s *Store) ClearTargetKey(id string) error {
	_, err := s.db.Exec(`UPDATE targets SET api_key_enc='', updated_at=? WHERE id=?`, model.NowMs(), id)
	return err
}

func (s *Store) TouchTarget(id string, modelCount int) error {
	_, err := s.db.Exec(`UPDATE targets SET last_tested_at=?, model_count=CASE WHEN ?>0 THEN ? ELSE model_count END WHERE id=?`,
		model.NowMs(), modelCount, modelCount, id)
	return err
}

func (s *Store) DeleteTarget(id string) error {
	_, err := s.db.Exec(`DELETE FROM targets WHERE id=?`, id)
	return err
}

// ---------- prompts ----------

func (s *Store) ListPrompts() ([]model.Prompt, error) {
	rows, err := s.db.Query(`SELECT id,name,category,content,builtin,created_at,updated_at FROM prompts ORDER BY builtin DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Prompt, 0)
	for rows.Next() {
		var p model.Prompt
		var builtin int
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Content, &builtin, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Builtin = builtin != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) GetPrompts(ids []string) ([]model.Prompt, error) {
	if len(ids) == 0 {
		return s.ListPrompts()
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT id,name,category,content,builtin,created_at,updated_at FROM prompts WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]model.Prompt{}
	for rows.Next() {
		var p model.Prompt
		var builtin int
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.Content, &builtin, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Builtin = builtin != 0
		byID[p.ID] = p
	}
	out := make([]model.Prompt, 0, len(ids))
	for _, id := range ids {
		if p, ok := byID[id]; ok {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}

func (s *Store) SavePrompt(p *model.Prompt) error {
	now := model.NowMs()
	if p.ID == "" {
		p.ID = model.NewID("p_")
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	if p.Category == "" {
		p.Category = "custom"
	}
	_, err := s.db.Exec(`INSERT INTO prompts(id,name,category,content,builtin,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, category=excluded.category, content=excluded.content, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Category, p.Content, boolInt(p.Builtin), p.CreatedAt, p.UpdatedAt)
	return err
}

// EnsureBuiltinPrompts inserts seed prompts only when their IDs are absent.
func (s *Store) EnsureBuiltinPrompts(seed []model.Prompt) error {
	for i := range seed {
		p := &seed[i]
		now := model.NowMs()
		if p.CreatedAt == 0 {
			p.CreatedAt = now
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = now
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO prompts(id,name,category,content,builtin,created_at,updated_at) VALUES(?,?,?,?,1,?,?)`,
			p.ID, p.Name, p.Category, p.Content, p.CreatedAt, p.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

// EnsureBuiltinAttacks inserts seed attacks only when their IDs are absent.
func (s *Store) EnsureBuiltinAttacks(seed []model.Attack) error {
	for i := range seed {
		a := &seed[i]
		now := model.NowMs()
		if a.CreatedAt == 0 {
			a.CreatedAt = now
		}
		if a.UpdatedAt == 0 {
			a.UpdatedAt = now
		}
		checks, _ := json.Marshal(a.Checks)
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO attacks(id,name,category,severity,system_prompt,prompt,canary,checks_json,require_judge,rubric,builtin,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,1,?,?)`,
			a.ID, a.Name, a.Category, a.Severity, a.SystemPrompt, a.Prompt, a.Canary, string(checks),
			boolInt(a.RequireJudge), a.Rubric, a.CreatedAt, a.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeletePrompt(id string) error {
	_, err := s.db.Exec(`DELETE FROM prompts WHERE id=? AND builtin=0`, id)
	return err
}

// ---------- attacks ----------

func (s *Store) ListAttacks() ([]model.Attack, error) {
	rows, err := s.db.Query(`SELECT id,name,category,severity,system_prompt,prompt,canary,checks_json,require_judge,rubric,builtin,created_at,updated_at FROM attacks ORDER BY category, severity DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttacks(rows)
}

func scanAttacks(rows *sql.Rows) ([]model.Attack, error) {
	out := make([]model.Attack, 0)
	for rows.Next() {
		var a model.Attack
		var checks string
		var builtin, reqJudge int
		if err := rows.Scan(&a.ID, &a.Name, &a.Category, &a.Severity, &a.SystemPrompt, &a.Prompt, &a.Canary, &checks, &reqJudge, &a.Rubric, &builtin, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.Builtin = builtin != 0
		a.RequireJudge = reqJudge != 0
		_ = json.Unmarshal([]byte(checks), &a.Checks)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAttacks(ids []string) ([]model.Attack, error) {
	if len(ids) == 0 {
		return s.ListAttacks()
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	rows, err := s.db.Query(`SELECT id,name,category,severity,system_prompt,prompt,canary,checks_json,require_judge,rubric,builtin,created_at,updated_at FROM attacks WHERE id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]model.Attack{}
	list, err := scanAttacks(rows)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		byID[a.ID] = a
	}
	out := make([]model.Attack, 0, len(ids))
	for _, id := range ids {
		if a, ok := byID[id]; ok {
			out = append(out, a)
		}
	}
	return out, nil
}

func (s *Store) SaveAttack(a *model.Attack) error {
	now := model.NowMs()
	if a.ID == "" {
		a.ID = model.NewID("a_")
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
	checks, _ := json.Marshal(a.Checks)
	_, err := s.db.Exec(`INSERT INTO attacks(id,name,category,severity,system_prompt,prompt,canary,checks_json,require_judge,rubric,builtin,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, category=excluded.category, severity=excluded.severity,
		system_prompt=excluded.system_prompt, prompt=excluded.prompt, canary=excluded.canary, checks_json=excluded.checks_json,
		require_judge=excluded.require_judge, rubric=excluded.rubric, updated_at=excluded.updated_at`,
		a.ID, a.Name, a.Category, a.Severity, a.SystemPrompt, a.Prompt, a.Canary, string(checks), boolInt(a.RequireJudge), a.Rubric, boolInt(a.Builtin), a.CreatedAt, a.UpdatedAt)
	return err
}

func (s *Store) DeleteAttack(id string) error {
	_, err := s.db.Exec(`DELETE FROM attacks WHERE id=? AND builtin=0`, id)
	return err
}

func (s *Store) ResetBuiltinAttacks(seed []model.Attack) error {
	if _, err := s.db.Exec(`DELETE FROM attacks WHERE builtin=1`); err != nil {
		return err
	}
	for i := range seed {
		seed[i].Builtin = true
		if err := s.SaveAttack(&seed[i]); err != nil {
			return err
		}
	}
	return nil
}

// ---------- prices ----------

func (s *Store) ListPrices() ([]model.Price, error) {
	rows, err := s.db.Query(`SELECT id,pattern,input_per_m,output_per_m,cache_read_per_m,cache_write_per_m,currency,note,updated_at FROM prices ORDER BY pattern`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Price, 0)
	for rows.Next() {
		var p model.Price
		if err := rows.Scan(&p.ID, &p.Pattern, &p.InputPerM, &p.OutputPerM, &p.CacheReadPerM, &p.CacheWritePerM, &p.Currency, &p.Note, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SavePrice(p *model.Price) error {
	if p.ID == "" {
		p.ID = model.NewID("pr_")
	}
	p.UpdatedAt = model.NowMs()
	if p.Currency == "" {
		p.Currency = "USD"
	}
	_, err := s.db.Exec(`INSERT INTO prices(id,pattern,input_per_m,output_per_m,cache_read_per_m,cache_write_per_m,currency,note,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET pattern=excluded.pattern, input_per_m=excluded.input_per_m, output_per_m=excluded.output_per_m,
		cache_read_per_m=excluded.cache_read_per_m, cache_write_per_m=excluded.cache_write_per_m, currency=excluded.currency,
		note=excluded.note, updated_at=excluded.updated_at`,
		p.ID, p.Pattern, p.InputPerM, p.OutputPerM, p.CacheReadPerM, p.CacheWritePerM, p.Currency, p.Note, p.UpdatedAt)
	return err
}

func (s *Store) DeletePrice(id string) error {
	_, err := s.db.Exec(`DELETE FROM prices WHERE id=?`, id)
	return err
}

// ---------- runs ----------

func (s *Store) CreateRun(r *model.Run) error {
	if r.CreatedAt == 0 {
		r.CreatedAt = model.NowMs()
	}
	if r.Config == "" {
		r.Config = "{}"
	}
	if r.Summary == "" {
		r.Summary = "{}"
	}
	_, err := s.db.Exec(`INSERT INTO runs(id,kind,status,config_json,summary_json,error,created_at,started_at,finished_at)
		VALUES(?,?,?,?,?,?,?,?,?)`, r.ID, r.Kind, r.Status, r.Config, r.Summary, r.Error, r.CreatedAt, r.StartedAt, r.FinishedAt)
	return err
}

func (s *Store) StartRun(id string) error {
	_, err := s.db.Exec(`UPDATE runs SET status=?, started_at=? WHERE id=?`, model.StatusRunning, model.NowMs(), id)
	return err
}

func (s *Store) FinishRun(id string, status model.RunStatus, summary any, errMsg string) error {
	raw, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE runs SET status=?, summary_json=?, error=?, finished_at=? WHERE id=?`,
		status, string(raw), errMsg, model.NowMs(), id)
	return err
}

func (s *Store) DeleteRun(id string) error {
	_, err := s.db.Exec(`DELETE FROM runs WHERE id=?`, id)
	return err
}

func (s *Store) GetRun(id string) (*model.Run, error) {
	var r model.Run
	err := s.db.QueryRow(`SELECT id,kind,status,config_json,summary_json,error,created_at,started_at,finished_at FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.Kind, &r.Status, &r.Config, &r.Summary, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if err != nil {
		return nil, err
	}
	if r.FinishedAt > 0 && r.StartedAt > 0 {
		r.DurationMs = r.FinishedAt - r.StartedAt
	}
	return &r, nil
}

type RunRow struct {
	model.Run
	Summary model.RunSummary `json:"summary"`
}

func (s *Store) ListRuns(kind string, limit, offset int) ([]RunRow, int, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	where := "1=1"
	args := []any{}
	if kind != "" {
		where = "kind=?"
		args = append(args, kind)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT id,kind,status,config_json,summary_json,error,created_at,started_at,finished_at FROM runs WHERE ` + where + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]RunRow, 0)
	for rows.Next() {
		var row RunRow
		r := &row.Run
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.Config, &r.Summary, &r.Error, &r.CreatedAt, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, 0, err
		}
		if r.FinishedAt > 0 && r.StartedAt > 0 {
			r.DurationMs = r.FinishedAt - r.StartedAt
		}
		_ = json.Unmarshal([]byte(r.Summary), &row.Summary)
		out = append(out, row)
	}
	return out, total, rows.Err()
}

func (s *Store) MarkInterrupted() error {
	_, err := s.db.Exec(`UPDATE runs SET status=?, error=?, finished_at=? WHERE status IN ('queued','running')`,
		model.StatusInterrupted, "服务重启导致测试中断", model.NowMs())
	return err
}

func (s *Store) PurgeBefore(cutoffMs int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM runs WHERE finished_at > 0 AND finished_at < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ---------- items ----------

const itemColumns = `id,run_id,item_index,attempts,target_id,target_name,model,protocol,case_id,case_name,category,severity,repetition,
	started_at,start_offset_ms,latency_ms,ttft_ms,generation_ms,tpot_ms,status,ok,prompt_tokens,completion_tokens,total_tokens,cached_tokens,
	cache_write_tokens,reasoning_tokens,local_prompt_tokens,token_source,cost,error_type,error_message,request_id,finish_reason,
	output_preview,output_text,request_json,response_json,verdict,judge_json,checks_json,meta_json,created_at`

func (s *Store) InsertItems(items []model.Item) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO run_items(run_id,item_index,attempts,target_id,target_name,model,protocol,case_id,case_name,category,severity,
		repetition,started_at,start_offset_ms,latency_ms,ttft_ms,generation_ms,tpot_ms,status,ok,prompt_tokens,completion_tokens,total_tokens,
		cached_tokens,cache_write_tokens,reasoning_tokens,local_prompt_tokens,token_source,cost,error_type,error_message,request_id,finish_reason,
		output_preview,output_text,request_json,response_json,verdict,judge_json,checks_json,meta_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for i := range items {
		it := &items[i]
		judge, _ := json.Marshal(it.Judge)
		checks, _ := json.Marshal(it.Checks)
		meta, _ := json.Marshal(it.Meta)
		if string(judge) == "null" {
			judge = []byte("")
		}
		if len(meta) == 0 || string(meta) == "null" {
			meta = []byte("{}")
		}
		if len(checks) == 0 || string(checks) == "null" {
			checks = []byte("[]")
		}
		if it.StartedAt == 0 {
			it.StartedAt = model.NowMs()
		}
		if _, err := stmt.Exec(it.RunID, it.Index, it.Attempts, it.TargetID, it.TargetName, it.Model, it.Protocol, it.CaseID, it.CaseName,
			it.Category, it.Severity, it.Repetition, it.StartedAt, it.StartOffsetMs, it.LatencyMs, it.TTFTMs, it.GenerationMs, it.TPOTMs,
			it.Status, boolInt(it.OK), it.PromptTokens, it.CompletionTokens, it.TotalTokens, it.CachedTokens, it.CacheWriteTokens,
			it.ReasoningTokens, it.LocalPromptTokens, it.TokenSource, it.Cost, it.ErrorType, it.ErrorMessage, it.RequestID, it.FinishReason,
			it.OutputPreview, it.OutputText, it.RequestJSON, it.ResponseJSON, it.Verdict, string(judge), string(checks), string(meta), it.StartedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type ItemFilter struct {
	RunID    string
	Model    string
	OnlyFail bool
	OnlyOK   bool
	Verdict  string
	Category string
	Search   string
	Limit    int
	Offset   int
	OrderAsc bool
}

func (s *Store) ListItems(f ItemFilter) ([]model.Item, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	where := []string{"run_id=?"}
	args := []any{f.RunID}
	if f.Model != "" {
		where = append(where, "model=?")
		args = append(args, f.Model)
	}
	if f.OnlyFail {
		where = append(where, "ok=0")
	}
	if f.OnlyOK {
		where = append(where, "ok=1")
	}
	if f.Verdict != "" {
		where = append(where, "verdict=?")
		args = append(args, f.Verdict)
	}
	if f.Category != "" {
		where = append(where, "category=?")
		args = append(args, f.Category)
	}
	if f.Search != "" {
		where = append(where, "(case_name LIKE ? OR model LIKE ? OR target_name LIKE ? OR error_message LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like, like, like)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM run_items WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "DESC"
	if f.OrderAsc {
		order = "ASC"
	}
	rows, err := s.db.Query(`SELECT `+itemColumns+` FROM run_items WHERE `+clause+` ORDER BY id `+order+` LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]model.Item, 0, f.Limit)
	for rows.Next() {
		it, err := scanItem(rows, true)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, it)
	}
	return out, total, rows.Err()
}

func (s *Store) GetItem(runID string, id int64) (*model.ItemDetail, error) {
	rows, err := s.db.Query(`SELECT `+itemColumns+` FROM run_items WHERE run_id=? AND id=?`, runID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	it, err := scanItem(rows, false)
	if err != nil {
		return nil, err
	}
	d := it.Detail()
	return &d, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanItem(rows scanner, light bool) (model.Item, error) {
	var it model.Item
	var judgeJSON, checksJSON, metaJSON string
	err := rows.Scan(&it.ID, &it.RunID, &it.Index, &it.Attempts, &it.TargetID, &it.TargetName, &it.Model, &it.Protocol,
		&it.CaseID, &it.CaseName, &it.Category, &it.Severity, &it.Repetition, &it.StartedAt, &it.StartOffsetMs, &it.LatencyMs,
		&it.TTFTMs, &it.GenerationMs, &it.TPOTMs, &it.Status, &it.OK, &it.PromptTokens, &it.CompletionTokens, &it.TotalTokens,
		&it.CachedTokens, &it.CacheWriteTokens, &it.ReasoningTokens, &it.LocalPromptTokens, &it.TokenSource, &it.Cost,
		&it.ErrorType, &it.ErrorMessage, &it.RequestID, &it.FinishReason, &it.OutputPreview, &it.OutputText, &it.RequestJSON,
		&it.ResponseJSON, &it.Verdict, &judgeJSON, &checksJSON, &metaJSON, &it.CreatedAt)
	if err != nil {
		return it, err
	}
	if judgeJSON != "" {
		var j model.JudgeVerdict
		if json.Unmarshal([]byte(judgeJSON), &j) == nil {
			it.Judge = &j
		}
	}
	_ = json.Unmarshal([]byte(checksJSON), &it.Checks)
	if metaJSON != "" {
		_ = json.Unmarshal([]byte(metaJSON), &it.Meta)
	}
	if light {
		it.OutputText = ""
		it.RequestJSON = ""
		it.ResponseJSON = ""
	}
	return it, nil
}

// ListItemsAll returns up to limit items for exports (limit <= 0 means all).
func (s *Store) ListItemsAll(runID string, limit int) ([]model.Item, error) {
	if limit <= 0 || limit > 200000 {
		limit = 200000
	}
	rows, err := s.db.Query(`SELECT `+itemColumns+` FROM run_items WHERE run_id=? ORDER BY id ASC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Item, 0, 128)
	for rows.Next() {
		it, err := scanItem(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) CountItems(runID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM run_items WHERE run_id=?`, runID).Scan(&n)
	return n, err
}

// ---------- request snapshots ----------

func (s *Store) InsertSnapshots(list []model.Snapshot) error {
	if len(list) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO request_snapshots(run_id,run_kind,item_index,case_id,case_name,target_id,target_name,
		target_label,model,protocol,endpoint,request_id,repetition,request_time_ms,body_sha256,body_size,body_text,canonical_text,
		fingerprints_json,findings_json,created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for i := range list {
		sn := &list[i]
		fp, _ := json.Marshal(sn.Fingerprints)
		findings, _ := json.Marshal(sn.Findings)
		if len(findings) == 0 {
			findings = []byte("[]")
		}
		if sn.CreatedAt == 0 {
			sn.CreatedAt = model.NowMs()
		}
		if _, err := stmt.Exec(sn.RunID, sn.RunKind, sn.ItemIndex, sn.CaseID, sn.CaseName, sn.TargetID, sn.TargetName,
			sn.TargetLabel, sn.Model, sn.Protocol, sn.Endpoint, sn.RequestID, sn.Repetition, sn.RequestTime,
			sn.BodySHA256, sn.BodySize, sn.Body, sn.Canonical, string(fp), string(findings), sn.CreatedAt); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type SnapshotFilter struct {
	RunID    string
	CaseID   string
	TargetID string
	Model    string
	Protocol string
	Search   string
	Limit    int
	Offset   int
	OrderAsc bool
}

func (s *Store) ListSnapshots(f SnapshotFilter) ([]model.Snapshot, int, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	if f.RunID != "" {
		where = append(where, "run_id=?")
		args = append(args, f.RunID)
	}
	if f.CaseID != "" {
		where = append(where, "case_id=?")
		args = append(args, f.CaseID)
	}
	if f.TargetID != "" {
		where = append(where, "target_id=?")
		args = append(args, f.TargetID)
	}
	if f.Model != "" {
		where = append(where, "model=?")
		args = append(args, f.Model)
	}
	if f.Protocol != "" {
		where = append(where, "protocol=?")
		args = append(args, f.Protocol)
	}
	if f.Search != "" {
		like := "%" + f.Search + "%"
		where = append(where, "(case_name LIKE ? OR model LIKE ? OR target_label LIKE ? OR request_id LIKE ? OR body_sha256 LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM request_snapshots WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "DESC"
	if f.OrderAsc {
		order = "ASC"
	}
	rows, err := s.db.Query(`SELECT id,run_id,run_kind,item_index,case_id,case_name,target_id,target_name,target_label,model,
		protocol,endpoint,request_id,repetition,request_time_ms,body_sha256,body_size,fingerprints_json,findings_json,created_at
		FROM request_snapshots WHERE `+clause+` ORDER BY id `+order+` LIMIT ? OFFSET ?`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]model.Snapshot, 0, f.Limit)
	for rows.Next() {
		sn, err := scanSnapshot(rows, false)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, sn)
	}
	return out, total, rows.Err()
}

func (s *Store) GetSnapshot(id int64) (*model.SnapshotDetail, error) {
	rows, err := s.db.Query(`SELECT id,run_id,run_kind,item_index,case_id,case_name,target_id,target_name,target_label,model,
		protocol,endpoint,request_id,repetition,request_time_ms,body_sha256,body_size,fingerprints_json,findings_json,created_at,
		body_text,canonical_text FROM request_snapshots WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	sn, err := scanSnapshot(rows, true)
	if err != nil {
		return nil, err
	}
	d := sn.Detail()
	return &d, nil
}

func scanSnapshot(rows scanner, withBody bool) (model.Snapshot, error) {
	var sn model.Snapshot
	var fpJSON, findingsJSON string
	dest := []any{&sn.ID, &sn.RunID, &sn.RunKind, &sn.ItemIndex, &sn.CaseID, &sn.CaseName, &sn.TargetID, &sn.TargetName,
		&sn.TargetLabel, &sn.Model, &sn.Protocol, &sn.Endpoint, &sn.RequestID, &sn.Repetition, &sn.RequestTime,
		&sn.BodySHA256, &sn.BodySize, &fpJSON, &findingsJSON, &sn.CreatedAt}
	if withBody {
		dest = append(dest, &sn.Body, &sn.Canonical)
	}
	if err := rows.Scan(dest...); err != nil {
		return sn, err
	}
	_ = json.Unmarshal([]byte(fpJSON), &sn.Fingerprints)
	if findingsJSON != "" {
		_ = json.Unmarshal([]byte(findingsJSON), &sn.Findings)
	}
	if sn.Findings == nil {
		sn.Findings = []model.Finding{}
	}
	return sn, nil
}

// ---------- settings ----------

func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
