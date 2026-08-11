package export

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Caps son los encoders que reporta el ffmpeg local.
type Caps struct {
	ProResVT bool
	HEVCVT   bool
	H264VT   bool
	VP9      bool
	X264     bool
	X265     bool
}

// DetectCaps consulta `ffmpeg -encoders` una vez. Sin ffmpeg (o con error)
// devuelve cero capacidades.
func DetectCaps(ctx context.Context) Caps {
	out, err := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-encoders").Output()
	if err != nil {
		return Caps{}
	}
	s := string(out)
	return Caps{
		ProResVT: strings.Contains(s, "prores_videotoolbox"),
		HEVCVT:   strings.Contains(s, "hevc_videotoolbox"),
		H264VT:   strings.Contains(s, "h264_videotoolbox"),
		VP9:      strings.Contains(s, "libvpx-vp9"),
		X264:     strings.Contains(s, "libx264"),
		X265:     strings.Contains(s, "libx265"),
	}
}

// arm64VT: los modos de calidad constante de VideoToolbox (-q:v,
// -alpha_quality) solo existen sobre Apple Silicon; en Macs Intel el encoder
// se lista pero aborta al inicializar.
func arm64VT() bool { return runtime.GOARCH == "arm64" }

// Preset es una receta de salida. hw es el camino VideoToolbox (medido ~3×
// más rápido con ~1/8 de CPU y calidad de masterización equivalente); sw el
// fallback por software; Seq marca salida como secuencia de PNGs (directorio)
// en vez de un archivo de video.
type Preset struct {
	Name string
	Ext  string
	Seq  bool
	hw   []string
	hwOK func(Caps) bool
	sw   []string
	swOK func(Caps) bool // nil = el camino sw no depende de encoders opcionales
}

// Args devuelve los args del encoder y si son el camino de hardware. Asume
// que Supported ya se validó.
func (p Preset) Args(c Caps) ([]string, bool) {
	if p.hw != nil && p.hwOK != nil && p.hwOK(c) {
		return p.hw, true
	}
	return p.sw, false
}

// Supported dice si el preset puede codificar con el ffmpeg local.
func (p Preset) Supported(c Caps) bool {
	if p.Seq {
		return true
	}
	if p.hw != nil && p.hwOK != nil && p.hwOK(c) {
		return true
	}
	return p.sw != nil && (p.swOK == nil || p.swOK(c))
}

var presets = []Preset{
	{
		Name: "prores4444",
		Ext:  ".mov",
		// -pix_fmt bgra deja pasar alfa si los frames lo traen; -allow_sw 1
		// cubre chips sin media engine ProRes (M1 base) sin cambiar el comando.
		hw:   []string{"-c:v", "prores_videotoolbox", "-profile:v", "4444", "-pix_fmt", "bgra", "-allow_sw", "1"},
		hwOK: func(c Caps) bool { return c.ProResVT },
		sw:   []string{"-c:v", "prores_ks", "-profile:v", "4444", "-pix_fmt", "yuv444p10le", "-qscale:v", "2", "-vendor", "apl0"},
	},
	{
		Name: "prores422hq",
		Ext:  ".mov",
		hw:   []string{"-c:v", "prores_videotoolbox", "-profile:v", "hq", "-allow_sw", "1"},
		hwOK: func(c Caps) bool { return c.ProResVT },
		sw:   []string{"-c:v", "prores_ks", "-profile:v", "3", "-pix_fmt", "yuv422p10le", "-qscale:v", "4", "-vendor", "apl0"},
	},
	{
		Name: "prores422",
		Ext:  ".mov",
		hw:   []string{"-c:v", "prores_videotoolbox", "-profile:v", "standard", "-allow_sw", "1"},
		hwOK: func(c Caps) bool { return c.ProResVT },
		sw:   []string{"-c:v", "prores_ks", "-profile:v", "2", "-pix_fmt", "yuv422p10le", "-qscale:v", "4", "-vendor", "apl0"},
	},
	{
		Name: "h264",
		Ext:  ".mp4",
		hw:   []string{"-c:v", "h264_videotoolbox", "-q:v", "75", "-pix_fmt", "yuv420p", "-movflags", "+faststart"},
		hwOK: func(c Caps) bool { return c.H264VT && arm64VT() },
		sw:   []string{"-c:v", "libx264", "-crf", "17", "-preset", "medium", "-pix_fmt", "yuv420p", "-movflags", "+faststart"},
		swOK: func(c Caps) bool { return c.X264 },
	},
	{
		Name: "hevc",
		Ext:  ".mp4",
		// -tag:v hvc1 es obligatorio para QuickTime/Safari.
		hw:   []string{"-c:v", "hevc_videotoolbox", "-q:v", "80", "-pix_fmt", "yuv420p", "-tag:v", "hvc1", "-movflags", "+faststart"},
		hwOK: func(c Caps) bool { return c.HEVCVT && arm64VT() },
		sw:   []string{"-c:v", "libx265", "-crf", "20", "-preset", "medium", "-pix_fmt", "yuv420p", "-tag:v", "hvc1", "-movflags", "+faststart"},
		swOK: func(c Caps) bool { return c.X265 },
	},
	{
		Name: "hevc-alpha",
		Ext:  ".mov",
		// Transparencia para Safari/iOS (~10× más liviano que ProRes 4444).
		// Solo VideoToolbox: libx265 no codifica alfa. ffprobe reportará
		// yuv420p aunque el alfa esté (viaja en capa auxiliar HEVC).
		hw:   []string{"-c:v", "hevc_videotoolbox", "-q:v", "65", "-alpha_quality", "0.75", "-pix_fmt", "bgra", "-tag:v", "hvc1", "-movflags", "+faststart"},
		hwOK: func(c Caps) bool { return c.HEVCVT && arm64VT() },
	},
	{
		Name: "webm-alpha",
		Ext:  ".webm",
		// Transparencia cross-browser (Chrome/Firefox/Edge; Safari no).
		// -row-mt 1 -cpu-used 4 son imprescindibles a 2160 (5× más rápido);
		// -auto-alt-ref 0 por compatibilidad con libvpx viejos.
		sw:   []string{"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-crf", "30", "-b:v", "0", "-deadline", "good", "-cpu-used", "4", "-row-mt", "1", "-auto-alt-ref", "0"},
		swOK: func(c Caps) bool { return c.VP9 },
	},
	{
		Name: "png-seq",
		Ext:  "", // OutputPath ya produce "<nombre>.png-seq" (directorio)
		Seq:  true,
		// Stream copy: los PNGs de captura van a disco bit-exactos, sin
		// re-codificar — el escape hatch para compositing en NLE/AE.
		sw: []string{"-f", "image2", "-c", "copy"},
	},
}

// Presets devuelve las recetas disponibles, la primera es la default.
func Presets() []Preset { return presets }

// PresetByName busca una receta por nombre.
func PresetByName(name string) (Preset, error) {
	names := make([]string, len(presets))
	for i, p := range presets {
		if p.Name == name {
			return p, nil
		}
		names[i] = p.Name
	}
	return Preset{}, fmt.Errorf("preset desconocido %q (hay: %s)", name, strings.Join(names, ", "))
}

// ffmpegArgs arma la línea completa de ffmpeg; el bool indica encoder de
// hardware. Para presets Seq, out es un directorio ya creado.
func ffmpegArgs(opt Options, out string) ([]string, bool) {
	enc, hw := opt.Preset.Args(opt.Caps)
	args := []string{"-y", "-loglevel", "error", "-f", "image2pipe", "-framerate", strconv.Itoa(opt.FPS), "-i", "-"}
	args = append(args, enc...)
	if opt.Preset.Seq {
		return append(args, filepath.Join(out, "f%05d.png")), hw
	}
	return append(args, out), hw
}
