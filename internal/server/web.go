package server

import (
	"net/http"
	"os"
	"path/filepath"
)

func registerWeb(mux *http.ServeMux, configuredDir string) {
	webDir := resolveWebDir(configuredDir)
	if webDir == "" {
		return
	}

	fileServer := http.FileServer(http.Dir(webDir))
	indexPath := filepath.Join(webDir, "index.html")

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/overview", "/nodes", "/users":
			http.ServeFile(w, r, indexPath)
		default:
			http.NotFound(w, r)
		}
	})

	mux.Handle("/app.wasm", fileServer)
	mux.Handle("/wasm_exec.js", fileServer)
	mux.Handle("/styles.css", fileServer)
}

func resolveWebDir(configuredDir string) string {
	candidates := make([]string, 0, 3)
	if configuredDir != "" {
		candidates = append(candidates, configuredDir)
	}
	candidates = append(candidates, "web/panel")
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates, filepath.Clean(filepath.Join(exeDir, "../share/pulse/web/panel")))
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}

	return ""
}
