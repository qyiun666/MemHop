package testsupport

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"memhop/api"
)

// LLM config environment variables. Priority: env vars > key_config.json file.
const (
	EnvLLMKey   = "MEMHOP_TEST_LLM_KEY"
	EnvLLMURL   = "MEMHOP_TEST_LLM_URL"
	EnvLLMModel = "MEMHOP_TEST_LLM_MODEL"
)

// Defaults used when only MEMHOP_TEST_LLM_KEY is set;
// kept in sync with key_config.json.example.
const (
	defaultLLMURL   = "https://api.deepseek.com/v1/chat/completions"
	defaultLLMModel = "deepseek-v4-flash"
)

// errNoLLMConfig is returned when neither env vars nor key_config.json
// provide an LLM API key.
var errNoLLMConfig = errors.New("testsupport: no LLM config: set " + EnvLLMKey +
	" or copy test/testsupport/key_config.json.example to test/testsupport/key_config.json")

func keyConfigPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "key_config.json")
}

// loadLLMConfig fills cfg.LLM from env vars first, then key_config.json.
// Returns errNoLLMConfig when neither source provides an API key.
func loadLLMConfig(cfg *memhop.Config) error {
	if key := os.Getenv(EnvLLMKey); key != "" {
		cfg.LLM.APIKey = key
		cfg.LLM.APIURL = os.Getenv(EnvLLMURL)
		if cfg.LLM.APIURL == "" {
			cfg.LLM.APIURL = defaultLLMURL
		}
		cfg.LLM.Model = os.Getenv(EnvLLMModel)
		if cfg.LLM.Model == "" {
			cfg.LLM.Model = defaultLLMModel
		}
		cfg.LLM.TimeoutSecs = 120
		return nil
	}

	f, err := os.Open(keyConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return errNoLLMConfig
		}
		return fmt.Errorf("testsupport: read key_config.json: %w", err)
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&cfg.LLM); err != nil {
		return fmt.Errorf("testsupport: parse key_config.json: %w", err)
	}
	if cfg.LLM.APIKey == "" {
		return errNoLLMConfig
	}
	return nil
}

// OpenMemHop opens a MemHop database backed by real services
// (Ollama encoder + LLM). The DB file lives in t.TempDir().
// It calls t.Skip when LLM config is missing or Ollama is unavailable,
// and t.Fatal on any other error. The caller must call Close() when done.
func OpenMemHop(t *testing.T) *memhop.MemHop {
	t.Helper()

	cfg := memhop.Config{
		DBPath:      filepath.Join(t.TempDir(), "test.meh"),
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
	}
	if err := loadLLMConfig(&cfg); err != nil {
		t.Skipf("跳过真实依赖测试: %v", err)
	}

	mh, err := memhop.Open(&cfg)
	if err != nil {
		if errors.Is(err, memhop.ErrEncoder) {
			t.Skipf("跳过真实依赖测试: Ollama 不可用: %v", err)
		}
		t.Fatalf("memhop.Open: %v", err)
	}
	return mh
}
