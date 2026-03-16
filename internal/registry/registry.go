package registry

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Registry struct {
	db *sql.DB
}

type Instance struct {
	DalID          string
	NodeID         string
	Template       string
	Status         string
	ContainerID    string
	RepoRoot       string
	ManifestPath   string
	ExportedSkills int
	CreatedAt      string
}

type Package struct {
	ID        int64
	UUID      string
	Name      string
	Category  string
	Version   string
	Status    string
	Checksum  string
	CreatedAt string
}

func Open(dbPath string) (*Registry, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open registry db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Registry{db: db}, nil
}

func (r *Registry) Close() error { return r.db.Close() }

func migrate(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS packages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		uuid TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		category TEXT,
		version TEXT,
		status TEXT DEFAULT 'registered',
		checksum TEXT,
		created_at TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS instances (
		dal_id TEXT PRIMARY KEY,
		node_id TEXT,
		template TEXT NOT NULL,
		status TEXT DEFAULT 'created',
		container_id TEXT,
		repo_root TEXT,
		manifest_path TEXT,
		exported_skills INTEGER DEFAULT 0,
		created_at TEXT NOT NULL
	);`
	if _, err := db.Exec(ddl); err != nil {
		return err
	}
	for _, stmt := range []string{
		"ALTER TABLE instances ADD COLUMN repo_root TEXT",
		"ALTER TABLE instances ADD COLUMN manifest_path TEXT",
		"ALTER TABLE instances ADD COLUMN exported_skills INTEGER DEFAULT 0",
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("dal-%x", b)
}

func (r *Registry) Join(template, repoRoot, manifestPath string, exportedSkills int) (*Instance, error) {
	inst := &Instance{
		DalID:          newID(),
		NodeID:         "",
		Template:       template,
		Status:         "ready",
		RepoRoot:       repoRoot,
		ManifestPath:   manifestPath,
		ExportedSkills: exportedSkills,
		CreatedAt:      time.Now().UTC().Format(time.RFC3339),
	}
	_, err := r.db.Exec(
		"INSERT INTO instances(dal_id,node_id,template,status,container_id,repo_root,manifest_path,exported_skills,created_at) VALUES(?,?,?,?,?,?,?,?,?)",
		inst.DalID, inst.NodeID, inst.Template, inst.Status, inst.ContainerID, inst.RepoRoot, inst.ManifestPath, inst.ExportedSkills, inst.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("join: %w", err)
	}
	return inst, nil
}

func (r *Registry) List() ([]Instance, error) {
	rows, err := r.db.Query("SELECT dal_id, node_id, template, status, container_id, repo_root, manifest_path, exported_skills, created_at FROM instances ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var i Instance
		if err := rows.Scan(&i.DalID, &i.NodeID, &i.Template, &i.Status, &i.ContainerID, &i.RepoRoot, &i.ManifestPath, &i.ExportedSkills, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *Registry) Status(name string) (*Instance, error) {
	var i Instance
	err := r.db.QueryRow(
		"SELECT dal_id, node_id, template, status, container_id, repo_root, manifest_path, exported_skills, created_at FROM instances WHERE dal_id=?", name,
	).Scan(&i.DalID, &i.NodeID, &i.Template, &i.Status, &i.ContainerID, &i.RepoRoot, &i.ManifestPath, &i.ExportedSkills, &i.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("instance %q not found", name)
		}
		return nil, err
	}
	return &i, nil
}
