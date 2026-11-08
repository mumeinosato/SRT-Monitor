package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// LoadInfo は読み込みの経緯。GUIとログに出す。
type LoadInfo struct {
	Source      string   // 実際に使ったファイル。既定値なら空
	FirstStart  bool     // ファイルが無く、既定値で始めた
	Notes       []string // 人に見せる理由(壊れたファイルの理由、bakへの切り替えなど)
	UnknownKeys []string // 無視した未知のキー
}

// Generations は config.toml.bak1 から bak3 までの世代数。
const Generations = 3

func bakPath(path string, n int) string { return fmt.Sprintf("%s.bak%d", path, n) }

// Parse はTOMLを既定値の上に読み、検証する。未知のキーは無視して返す。
func Parse(data []byte) (Config, []string, error) {
	cfg := Default()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return Config{}, nil, fmt.Errorf("TOML を読めない: %w", err)
	}
	var unknown []string
	dec := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	probe := Default()
	if err := dec.Decode(&probe); err != nil {
		var sme *toml.StrictMissingError
		if errors.As(err, &sme) {
			for _, de := range sme.Errors {
				unknown = append(unknown, strings.Join(de.Key(), "."))
			}
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, unknown, err
	}
	return cfg, unknown, nil
}

func loadFile(path string) (Config, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, nil, err
	}
	return Parse(data)
}

// Load は path を読む。壊れていれば bak1→bak2→bak3 の順に試し、
// 全部失敗したら既定値を返す。返り値の error は常に nil(理由は LoadInfo.Notes)。
func Load(path string) (Config, *LoadInfo) {
	info := &LoadInfo{}
	candidates := []string{path}
	for i := 1; i <= Generations; i++ {
		candidates = append(candidates, bakPath(path, i))
	}

	anyExists := false
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			anyExists = true
			break
		}
	}
	if !anyExists {
		info.FirstStart = true
		info.Notes = append(info.Notes, "設定ファイルが無いので既定値で起動した")
		return Default(), info
	}

	for i, c := range candidates {
		cfg, unknown, err := loadFile(c)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				if i == 0 {
					info.Notes = append(info.Notes, fmt.Sprintf("%s が無い", filepath.Base(c)))
				}
				continue
			}
			info.Notes = append(info.Notes, fmt.Sprintf("%s を使えない: %v", filepath.Base(c), err))
			continue
		}
		info.Source = c
		info.UnknownKeys = unknown
		if len(unknown) > 0 {
			info.Notes = append(info.Notes, "未知のキーを無視した: "+strings.Join(unknown, ", "))
		}
		if i > 0 {
			info.Notes = append(info.Notes, fmt.Sprintf("%s で起動した", filepath.Base(c)))
		}
		return cfg, info
	}
	info.Notes = append(info.Notes, "すべての世代が使えないので既定値で起動した")
	return Default(), info
}

// Marshal はTOMLへ直す。
func Marshal(cfg Config) ([]byte, error) {
	return toml.Marshal(cfg)
}

// Save は検証してから、一時ファイル→rename で保存する。
// 入れ替え前に現在のファイルを bak1 へ回し、bak1→bak2→bak3 とずらす。
// 内容が現在のファイルと同じなら世代はずらさない。
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return nil
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 後は無害
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}

	// 世代をずらす: bak2→bak3, bak1→bak2, 現在→bak1(コピー。現在のファイルは残す)
	for i := Generations; i >= 2; i-- {
		if err := os.Rename(bakPath(path, i-1), bakPath(path, i)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := copyFile(path, bakPath(path, 1)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, path)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// ---- プロファイル ----

// ProfileDir は設定ファイルと同じ場所の profiles/。
func ProfileDir(configFile string) string {
	return filepath.Join(filepath.Dir(configFile), "profiles")
}

func profileFile(configFile, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\.`) {
		return "", fmt.Errorf("プロファイル名が不正: %q", name)
	}
	return filepath.Join(ProfileDir(configFile), name+".toml"), nil
}

// ListProfiles は保存済みのプロファイル名を返す。
func ListProfiles(configFile string) []string {
	ents, err := os.ReadDir(ProfileDir(configFile))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if n, ok := strings.CutSuffix(e.Name(), ".toml"); ok && !e.IsDir() {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// SaveProfile は設定をプロファイルとして保存する。
func SaveProfile(configFile, name string, cfg Config) error {
	p, err := profileFile(configFile, name)
	if err != nil {
		return err
	}
	return Save(p, cfg)
}

// LoadProfile はプロファイルを読む(壊れていれば世代を試す)。
func LoadProfile(configFile, name string) (Config, error) {
	p, err := profileFile(configFile, name)
	if err != nil {
		return Config{}, err
	}
	if _, err := os.Stat(p); err != nil {
		return Config{}, err
	}
	cfg, info := Load(p)
	if info.Source == "" {
		return Config{}, fmt.Errorf("プロファイル %q を読めない: %s", name, strings.Join(info.Notes, "; "))
	}
	return cfg, nil
}
