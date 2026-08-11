// Package discover expande argumentos (archivos o directorios) a la lista de
// proyectos .dc.html a exportar.
package discover

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Expand acepta rutas a archivos .dc.html o directorios (que recorre en
// profundidad, ignorando carpetas ocultas y node_modules) y devuelve rutas
// absolutas sin duplicados. Los archivos pasados directo conservan su orden;
// lo hallado en directorios sale ordenado.
func Expand(args []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	for _, a := range args {
		fi, err := os.Stat(a)
		if err != nil {
			return nil, err
		}
		if !fi.IsDir() {
			if !strings.HasSuffix(a, ".dc.html") {
				return nil, fmt.Errorf("%s no es un proyecto .dc.html", a)
			}
			add(a)
			continue
		}
		var found []string
		err = filepath.WalkDir(a, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() && p != a && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return fs.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(name, ".dc.html") {
				found = append(found, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(found)
		for _, p := range found {
			add(p)
		}
	}
	return out, nil
}
