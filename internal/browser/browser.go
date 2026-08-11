// Package browser localiza o aprovisiona el motor de render (Blink).
//
// Orden de resolución, sin tocar la red: ruta explícita (--browser /
// DCX_BROWSER) → chrome-headless-shell cacheado (versión pineada de "Chrome
// for Testing") → navegador del sistema (Chrome, Chromium, Edge, Brave). Si
// no hay ninguno, Download trae el headless-shell pineado (~95 MB, una sola
// vez) al caché del usuario. Se pinea la versión para que un mismo proyecto
// produzca los mismos píxeles en cualquier máquina.
package browser

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// PinnedVersion es el chrome-headless-shell que dcx descarga y prefiere.
// Actualizarla a mano contra
// https://googlechromelabs.github.io/chrome-for-testing/LATEST_RELEASE_STABLE
const PinnedVersion = "151.0.7922.77"

// Source indica de dónde salió el binario resuelto.
type Source string

const (
	SourceExplicit Source = "ruta explícita"
	SourceCache    Source = "headless-shell"
	SourceSystem   Source = "navegador del sistema"
)

// Resolution es un navegador utilizable. Path vacío = no se encontró ninguno.
type Resolution struct {
	Path   string
	Source Source
}

var errUnsupportedPlatform = errors.New("plataforma sin build de chrome-headless-shell (usa --browser con tu propio Chrome/Chromium)")

func platformID() (string, error) {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return "mac-arm64", nil
	case "darwin/amd64":
		return "mac-x64", nil
	case "linux/amd64":
		return "linux64", nil
	case "windows/amd64":
		return "win64", nil
	default:
		return "", errUnsupportedPlatform
	}
}

func zipURL(version, platform string) string {
	return fmt.Sprintf("https://storage.googleapis.com/chrome-for-testing-public/%s/%s/chrome-headless-shell-%s.zip", version, platform, platform)
}

// CacheDir es la raíz de caché de dcx (~/Library/Caches/dcx en macOS,
// $XDG_CACHE_HOME/dcx en Linux).
func CacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "dcx"), nil
}

// shellBinary es la ruta esperada del binario cacheado para la versión pineada.
func shellBinary() (string, error) {
	platform, err := platformID()
	if err != nil {
		return "", err
	}
	cache, err := CacheDir()
	if err != nil {
		return "", err
	}
	name := "chrome-headless-shell"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(cache, "headless-shell", PinnedVersion, "chrome-headless-shell-"+platform, name), nil
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0)
}

// Resolve encuentra un navegador sin tocar la red. Con explicit != "" esa
// ruta es obligatoria (error si no existe). Path vacío y error nil significa
// "no hay ninguno; considera Download".
func Resolve(explicit string) (Resolution, error) {
	if explicit != "" {
		if !isExecutable(explicit) {
			return Resolution{}, fmt.Errorf("el navegador indicado no existe o no es ejecutable: %s", explicit)
		}
		return Resolution{Path: explicit, Source: SourceExplicit}, nil
	}
	if bin, err := shellBinary(); err == nil && isExecutable(bin) {
		return Resolution{Path: bin, Source: SourceCache}, nil
	}
	if bin := systemBrowser(); bin != "" {
		return Resolution{Path: bin, Source: SourceSystem}, nil
	}
	return Resolution{}, nil
}

func systemBrowser() string {
	switch runtime.GOOS {
	case "darwin":
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
		} {
			if isExecutable(p) {
				return p
			}
		}
	default:
		for _, name := range []string{
			"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
			"microsoft-edge", "brave-browser", "chrome",
		} {
			if p, err := exec.LookPath(name); err == nil {
				return p
			}
		}
	}
	return ""
}

// DownloadSize es el tamaño aproximado de la descarga, para mensajes de UI.
const DownloadSize = "~95 MB"

// Download descarga y descomprime el headless-shell pineado en el caché.
// Idempotente: si ya está, devuelve la ruta sin red. progress puede ser nil;
// total puede ser <= 0 si el servidor no lo anuncia.
func Download(ctx context.Context, progress func(done, total int64)) (string, error) {
	bin, err := shellBinary()
	if err != nil {
		return "", err
	}
	if isExecutable(bin) {
		return bin, nil
	}
	platform, err := platformID()
	if err != nil {
		return "", err
	}
	versionDir := filepath.Dir(filepath.Dir(bin))
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		return "", err
	}
	sweepLeftovers(versionDir)

	// Cota dura para conexiones colgadas; generosa para enlaces lentos.
	ctx, cancelDL := context.WithTimeout(ctx, 15*time.Minute)
	defer cancelDL()

	tmpZip, err := os.CreateTemp(versionDir, ".download-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmpZip.Name())
	defer tmpZip.Close()

	url := zipURL(PinnedVersion, platform)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("descargando %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("descargando %s: HTTP %d", url, resp.StatusCode)
	}
	body := io.Reader(resp.Body)
	if progress != nil {
		body = io.TeeReader(resp.Body, &progressWriter{total: resp.ContentLength, fn: progress})
	}
	if _, err := io.Copy(tmpZip, body); err != nil {
		return "", fmt.Errorf("descargando %s: %w", url, err)
	}
	if err := tmpZip.Close(); err != nil {
		return "", err
	}

	extractDir, err := os.MkdirTemp(versionDir, ".extract-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(extractDir)
	if err := unzip(tmpZip.Name(), extractDir); err != nil {
		return "", fmt.Errorf("descomprimiendo: %w", err)
	}
	// Validar ANTES del rename: nunca commitear un árbol sin binario al caché.
	src := filepath.Join(extractDir, "chrome-headless-shell-"+platform)
	if !isExecutable(filepath.Join(src, filepath.Base(bin))) {
		return "", fmt.Errorf("descarga inválida: el zip no trae %s ejecutable", filepath.Base(bin))
	}
	if isExecutable(bin) {
		return bin, nil // otra invocación completó la instalación mientras tanto
	}
	// Auto-reparación: un destino presente pero sin binario válido (p.ej. un
	// limpiador de disco borró el ejecutable) bloquearía el rename para
	// siempre — os.Rename sobre un directorio existente falla en macOS/Linux.
	dest := filepath.Dir(bin)
	if _, err := os.Stat(dest); err == nil {
		if err := os.RemoveAll(dest); err != nil {
			return "", fmt.Errorf("apartando instalación corrupta: %w", err)
		}
	}
	// Rename atómico; si otra invocación ganó la carrera, su copia vale igual.
	if err := os.Rename(src, dest); err != nil && !isExecutable(bin) {
		return "", err
	}
	if !isExecutable(bin) {
		return "", fmt.Errorf("descarga corrupta: falta %s", bin)
	}
	return bin, nil
}

// sweepLeftovers borra temporales de descargas muertas (SIGKILL, corte de
// luz). Solo los de más de una hora: uno reciente puede ser de otra
// invocación en curso.
func sweepLeftovers(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, ".download-") && !strings.HasPrefix(name, ".extract-") {
			continue
		}
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > time.Hour {
			os.RemoveAll(filepath.Join(dir, name))
		}
	}
}

type progressWriter struct {
	done  int64
	total int64
	fn    func(done, total int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.done += int64(len(p))
	w.fn(w.done, w.total)
	return len(p), nil
}

// unzip descomprime a dest preservando permisos, rechazando rutas fuera de
// dest (zip-slip) y symlinks (el zip de headless-shell no trae ninguno).
func unzip(zipPath, dest string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	destRoot := filepath.Clean(dest) + string(os.PathSeparator)
	for _, f := range r.File {
		target := filepath.Join(dest, filepath.FromSlash(f.Name))
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), destRoot) {
			return fmt.Errorf("entrada fuera del destino: %s", f.Name)
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink inesperado en el zip: %s", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		perm := mode.Perm()
		if perm == 0 {
			perm = 0o644
		}
		if err := extractFile(f, target, perm); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

func extractFile(f *zip.File, target string, perm os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
