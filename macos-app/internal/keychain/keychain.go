package keychain

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

type Store struct{ Binary string }

func (s Store) run(operation, account, secret string) (string, error) {
	if s.Binary == "" {
		return "", errors.New("Keychain helper is unavailable")
	}
	cmd := exec.Command(s.Binary, operation, account)
	if secret != "" {
		cmd.Stdin = strings.NewReader(secret)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
func (s Store) Set(account, secret string) error   { _, err := s.run("set", account, secret); return err }
func (s Store) Get(account string) (string, error) { return s.run("get", account, "") }
func (s Store) Delete(account string) error        { _, err := s.run("delete", account, ""); return err }
