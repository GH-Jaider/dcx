package export

import (
	"fmt"
	"strconv"
)

// Preset es una receta de codificación de ffmpeg.
type Preset struct {
	Name string
	Ext  string
	Args []string
}

var presets = []Preset{
	{
		Name: "prores4444",
		Ext:  ".mov",
		Args: []string{"-c:v", "prores_ks", "-profile:v", "4444", "-pix_fmt", "yuv444p10le", "-qscale:v", "2", "-vendor", "apl0"},
	},
	{
		Name: "prores422hq",
		Ext:  ".mov",
		Args: []string{"-c:v", "prores_ks", "-profile:v", "3", "-pix_fmt", "yuv422p10le", "-qscale:v", "4", "-vendor", "apl0"},
	},
	{
		Name: "h264",
		Ext:  ".mp4",
		Args: []string{"-c:v", "libx264", "-crf", "17", "-preset", "medium", "-pix_fmt", "yuv420p", "-movflags", "+faststart"},
	},
}

// Presets devuelve las recetas disponibles, la primera es la default.
func Presets() []Preset { return presets }

// PresetByName busca una receta por nombre.
func PresetByName(name string) (Preset, error) {
	for _, p := range presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, fmt.Errorf("preset desconocido %q (hay: prores4444, prores422hq, h264)", name)
}

func ffmpegArgs(opt Options, out string) []string {
	args := []string{"-y", "-loglevel", "error", "-f", "image2pipe", "-framerate", strconv.Itoa(opt.FPS), "-i", "-"}
	args = append(args, opt.Preset.Args...)
	return append(args, out)
}
