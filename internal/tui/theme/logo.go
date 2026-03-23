package theme

// Brand asset paths (canonical source logos).
const (
	BrandMarkSVGPath     = "assets/brand/orb_mark.svg"
	BrandFullLogoSVGPath = "assets/brand/orb_full_logo.svg"
	BrandFaviconSVGPath  = "assets/brand/orb_favicon_32.svg"
)

// HeaderMarkGlyph is a compact terminal-safe mark for one-line header usage.
const HeaderMarkGlyph = "◉"

// SplashMarkLines is a terminal-safe block-art mark derived from orb_mark.svg.
var SplashMarkLines = []string{
	"▗▅▅▅▅▆▆▆▅▃",
	"▜█████████▙",
	"▗███████████▌",
	"▐███████████▘",
	"▝██████████▘",
	"▀▀███▛▀▘",
}
