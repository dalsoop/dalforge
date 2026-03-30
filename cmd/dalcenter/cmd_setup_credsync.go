package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Setup dalcenter infrastructure",
	}
	cmd.AddCommand(newSetupCredSyncCmd())
	return cmd
}

func newSetupCredSyncCmd() *cobra.Command {
	var (
		softServeAddr string
		repoName      string
		clonePath     string
		hostPubKey    string
	)

	cmd := &cobra.Command{
		Use:   "cred-sync",
		Short: "Setup git-based credential sync via soft-serve",
		Long: `Sets up credential sync infrastructure:

  1. Creates dal-credentials repo on soft-serve
  2. Registers host SSH key as collaborator (if --host-pubkey given)
  3. Clones repo locally
  4. Pushes current credentials as initial commit
  5. Adds DALCENTER_CRED_GIT_REPO to dalcenter env files

After running this, configure the host to periodically push refreshed
credentials to soft-serve (see dal-credential-sync script).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetupCredSync(softServeAddr, repoName, clonePath, hostPubKey)
		},
	}

	cmd.Flags().StringVar(&softServeAddr, "soft-serve", "localhost:23231", "soft-serve SSH address (host:port)")
	cmd.Flags().StringVar(&repoName, "repo", "dal-credentials", "soft-serve repository name")
	cmd.Flags().StringVar(&clonePath, "clone-path", "/root/.dalcenter-credentials", "local clone path")
	cmd.Flags().StringVar(&hostPubKey, "host-pubkey", "", "PVE host SSH public key to register (optional)")
	return cmd
}

func runSetupCredSync(softServeAddr, repoName, clonePath, hostPubKey string) error {
	sshHost, sshPort := parseSoftServeAddr(softServeAddr)

	// Step 1: Check soft-serve is reachable
	fmt.Print("1. soft-serve 연결 확인... ")
	if err := sshSoftServe(sshHost, sshPort, "info"); err != nil {
		fmt.Println("✗")
		return fmt.Errorf("soft-serve 접속 실패 (%s): %w\n  soft-serve가 실행 중인지, SSH 키가 등록되어 있는지 확인하세요", softServeAddr, err)
	}
	fmt.Println("✓")

	// Step 2: Create repo on soft-serve (ignore error if exists)
	fmt.Printf("2. soft-serve 레포 생성 (%s)... ", repoName)
	if err := sshSoftServe(sshHost, sshPort, "repo", "create", repoName); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Println("이미 존재")
		} else {
			fmt.Println("✗")
			return fmt.Errorf("레포 생성 실패: %w", err)
		}
	} else {
		fmt.Println("✓")
	}

	// Step 3: Register host pubkey
	if hostPubKey != "" {
		fmt.Print("3. 호스트 SSH 키 등록... ")
		// Try to create user first (ignore error if exists)
		_ = sshSoftServe(sshHost, sshPort, "user", "create", "pve-host")
		if err := sshSoftServe(sshHost, sshPort, "user", "add-pubkey", "pve-host", hostPubKey); err != nil {
			if !strings.Contains(err.Error(), "already") {
				fmt.Println("✗")
				return fmt.Errorf("키 등록 실패: %w", err)
			}
		}
		_ = sshSoftServe(sshHost, sshPort, "repo", "collab", "add", repoName, "pve-host")
		fmt.Println("✓")
	} else {
		fmt.Println("3. 호스트 SSH 키 등록... 생략 (--host-pubkey 미지정)")
	}

	// Step 4: Clone repo
	fmt.Printf("4. 로컬 클론 (%s)... ", clonePath)
	if _, err := os.Stat(filepath.Join(clonePath, ".git")); err == nil {
		fmt.Println("이미 존재")
	} else {
		repoURL := fmt.Sprintf("ssh://%s:%s/%s.git", sshHost, sshPort, repoName)
		out, err := exec.Command("git", "clone", repoURL, clonePath).CombinedOutput()
		if err != nil {
			fmt.Println("✗")
			return fmt.Errorf("git clone 실패: %w\n%s", err, string(out))
		}
		fmt.Println("✓")
	}

	// Step 5: Push initial credentials
	fmt.Print("5. 초기 credential push... ")
	changed, err := pushInitialCredentials(clonePath)
	if err != nil {
		fmt.Println("✗")
		return fmt.Errorf("초기 push 실패: %w", err)
	}
	if changed {
		fmt.Println("✓")
	} else {
		fmt.Println("변경 없음")
	}

	// Step 6: Add env to dalcenter env files
	fmt.Print("6. dalcenter env 설정... ")
	count := addCredGitEnv(clonePath)
	if count > 0 {
		fmt.Printf("✓ (%d개 파일 업데이트)\n", count)
	} else {
		fmt.Println("이미 설정됨")
	}

	fmt.Println()
	fmt.Println("=== 설치 완료 ===")
	fmt.Println()
	fmt.Println("다음 단계 (PVE 호스트에서 실행):")
	fmt.Println()
	fmt.Println("  # 1. credential sync 스크립트 설치")
	fmt.Println("  #    /usr/local/bin/dal-credential-sync 를 호스트에 복사")
	fmt.Println()
	fmt.Println("  # 2. systemd timer 등록")
	fmt.Println("  systemctl enable --now dal-credential-sync.timer")
	fmt.Println()
	fmt.Println("  # 3. 수동 테스트")
	fmt.Println("  dal-credential-sync")
	fmt.Println()
	fmt.Println("  # 4. dalcenter 서비스 재시작 (env 반영)")
	fmt.Println("  pct exec 105 -- systemctl restart 'dalcenter@*'")

	return nil
}

func parseSoftServeAddr(addr string) (string, string) {
	host, port, found := strings.Cut(addr, ":")
	if !found {
		return addr, "23231"
	}
	return host, port
}

func sshSoftServe(host, port string, args ...string) error {
	sshArgs := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "ConnectTimeout=5",
		"-p", port,
		host,
	}
	sshArgs = append(sshArgs, args...)
	out, err := exec.Command("ssh", sshArgs...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func pushInitialCredentials(clonePath string) (bool, error) {
	home, _ := os.UserHomeDir()
	files := map[string]string{
		filepath.Join(home, ".claude", ".credentials.json"): filepath.Join(clonePath, "claude", ".credentials.json"),
		filepath.Join(home, ".codex", "auth.json"):          filepath.Join(clonePath, "codex", "auth.json"),
	}

	changed := false
	for src, dst := range files {
		data, err := os.ReadFile(src)
		if err != nil || len(data) < 10 {
			continue
		}
		os.MkdirAll(filepath.Dir(dst), 0755)

		// Check if content differs
		existing, _ := os.ReadFile(dst)
		if string(existing) == string(data) {
			continue
		}

		if err := os.WriteFile(dst, data, 0600); err != nil {
			return false, err
		}
		changed = true
	}

	if !changed {
		return false, nil
	}

	// Git commit and push
	gitCmd := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-C", clonePath}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}

	_ = gitCmd("config", "user.name", "dalcenter")
	_ = gitCmd("config", "user.email", "dalcenter@local")
	_ = gitCmd("add", "-A")
	if err := gitCmd("commit", "-m", "init: credential sync setup"); err != nil {
		return false, err
	}
	if err := gitCmd("push", "origin", "main"); err != nil {
		return false, err
	}
	return true, nil
}

func addCredGitEnv(clonePath string) int {
	envDir := "/etc/dalcenter"
	entries, err := os.ReadDir(envDir)
	if err != nil {
		return 0
	}

	envLine := fmt.Sprintf("DALCENTER_CRED_GIT_REPO=%s", clonePath)
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		path := filepath.Join(envDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "DALCENTER_CRED_GIT_REPO") {
			continue
		}
		content := strings.TrimRight(string(data), "\n") + "\n" + envLine + "\n"
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			continue
		}
		count++
	}
	return count
}
