package testsupport

import (
	"encoding/json"
	"github.com/qyiun666/memhop/memhop"
	"os"
	"path/filepath"
	"runtime"
)

func keyConfigPath() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "key_config.json")
}

// Open opens a MemHop database for testing and closes it.
func Open() {
	mh := OpenMemHop()
	if err := mh.Close(); err != nil {
		panic(err)
	}
}

// OpenMemHop opens a MemHop database for testing.
// The caller must call Close() when done.
func OpenMemHop() *memhop.MemHop {
	f, err := os.Open(keyConfigPath())
	if err != nil {
		panic(err)
	}
	defer f.Close()

	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}

	dbPath := filepath.Join(home, ".test", "t.meh")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		panic(err)
	}

	cfg := memhop.Config{
		DBPath:      dbPath,
		VectorDim:   1024,
		EncoderAddr: "http://127.0.0.1:11434",
		EmbedModel:  "qllama/bge-m3:q4_k_m",
	}
	if err := json.NewDecoder(f).Decode(&cfg.LLM); err != nil {
		panic(err)
	}

	mh, err := memhop.Open(&cfg)
	if err != nil {
		panic(err)
	}
	return mh
}
