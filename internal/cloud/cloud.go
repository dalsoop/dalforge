package cloud

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config describes dalforge cloud registry access.
type Config struct {
	BaseURL   string
	Token     string
	GroupPath string
}

// LoadConfig reads dalforge cloud config from environment.
func LoadConfig() Config {
	baseURL := os.Getenv("DALFORGE_URL")
	if baseURL == "" {
		baseURL = os.Getenv("DAL_GITLAB_URL")
	}
	if baseURL == "" {
		baseURL = "https://gitlab.60.internal.kr"
	}

	token := os.Getenv("DALFORGE_TOKEN")
	if token == "" {
		token = os.Getenv("DAL_GITLAB_TOKEN")
	}

	groupPath := os.Getenv("DALFORGE_GROUP_PATH")
	if groupPath == "" {
		groupPath = os.Getenv("DAL_GROUP_PATH")
	}
	if groupPath == "" {
		groupPath = "dalforge-hub/dalcli"
	}

	return Config{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		GroupPath: groupPath,
	}
}

// PackageInfo describes one dalforge package visible in the cloud catalog.
type PackageInfo struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path"`
	Description   string `json:"description"`
	DefaultBranch string `json:"default_branch"`
	WebURL        string `json:"web_url"`
	CloneURL      string `json:"http_url_to_repo"`
}

// Client is a minimal dalforge cloud client backed by GitLab group/project APIs.
type Client struct {
	cfg    Config
	client *http.Client
}

// New creates a new cloud client.
func New(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

// Search finds matching packages in the configured dalforge catalog group.
func (c *Client) Search(ctx context.Context, query string) ([]PackageInfo, error) {
	groupPath := url.PathEscape(c.cfg.GroupPath)
	rawURL := fmt.Sprintf("%s/api/v4/groups/%s/projects?search=%s&per_page=20",
		c.cfg.BaseURL, groupPath, url.QueryEscape(query))
	return c.fetchProjects(ctx, rawURL)
}

// ListAll lists packages from the configured group.
func (c *Client) ListAll(ctx context.Context) ([]PackageInfo, error) {
	groupPath := url.PathEscape(c.cfg.GroupPath)
	rawURL := fmt.Sprintf("%s/api/v4/groups/%s/projects?per_page=100",
		c.cfg.BaseURL, groupPath)
	return c.fetchProjects(ctx, rawURL)
}

// Resolve resolves a short or full package name.
func (c *Client) Resolve(ctx context.Context, name string) (*PackageInfo, error) {
	candidates := []string{name}
	if !strings.HasPrefix(name, "dalcli-") {
		candidates = append(candidates, "dalcli-"+name)
	}
	for _, candidate := range candidates {
		pkgs, err := c.Search(ctx, candidate)
		if err != nil {
			return nil, err
		}
		for _, pkg := range pkgs {
			if pkg.Path == candidate || pkg.Name == candidate {
				return &pkg, nil
			}
		}
	}

	pkgs, err := c.Search(ctx, name)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("package %q not found in dalforge", name)
	}
	if len(pkgs) == 1 {
		return &pkgs[0], nil
	}

	names := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		names = append(names, pkg.Path)
	}
	return nil, fmt.Errorf("ambiguous package %q, matches: %s", name, strings.Join(names, ", "))
}

// Stage downloads and extracts a package archive into cacheRoot/<package-path>.
func (c *Client) Stage(ctx context.Context, name, cacheRoot string) (string, *PackageInfo, error) {
	pkg, err := c.Resolve(ctx, name)
	if err != nil {
		return "", nil, err
	}

	dest := filepath.Join(cacheRoot, pkg.Path)
	tmp := dest + ".tmp"
	if err := os.RemoveAll(tmp); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		return "", nil, err
	}
	if err := c.downloadArchive(ctx, *pkg, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	if err := os.RemoveAll(dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, err
	}
	return dest, pkg, nil
}

func (c *Client) fetchProjects(ctx context.Context, rawURL string) ([]PackageInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if c.cfg.Token != "" {
		req.Header.Set("PRIVATE-TOKEN", c.cfg.Token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dalforge unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("dalforge error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var projects []PackageInfo
	if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
		return nil, err
	}
	return projects, nil
}

func (c *Client) downloadArchive(ctx context.Context, pkg PackageInfo, dest string) error {
	branch := pkg.DefaultBranch
	if branch == "" {
		branch = "main"
	}
	rawURL := fmt.Sprintf("%s/api/v4/projects/%d/repository/archive.tar.gz?sha=%s",
		c.cfg.BaseURL, pkg.ID, url.QueryEscape(branch))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if c.cfg.Token != "" {
		req.Header.Set("PRIVATE-TOKEN", c.cfg.Token)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("download package archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("download package archive: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("read archive gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive tar: %w", err)
		}
		rel, ok := stripTopLevel(hdr.Name)
		if !ok || rel == "" {
			continue
		}
		target := filepath.Join(dest, rel)
		clean := filepath.Clean(target)
		if !strings.HasPrefix(clean, dest+string(os.PathSeparator)) && clean != dest {
			return fmt.Errorf("archive entry escapes destination: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(clean), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(clean, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}

func stripTopLevel(name string) (string, bool) {
	name = filepath.ToSlash(strings.TrimPrefix(name, "./"))
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return "", false
	}
	return filepath.Join(parts[1:]...), true
}
