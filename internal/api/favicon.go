package api

import (
	_ "embed"
	"net/http"
)

// Browser favicon assets, embedded so they ship in the single server binary.
// Light scheme uses the monochrome tray "S"; dark scheme uses the full Samo
// logo. Both are served publicly (the browser fetches the icon before login).
//
//go:embed assets/favicon-light.png
var faviconLightPNG []byte

//go:embed assets/favicon-dark.png
var faviconDarkPNG []byte

// serveFavicon writes the given PNG with a long, immutable cache lifetime —
// the icon effectively never changes and browsers refetch favicons eagerly.
func serveFavicon(png []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		_, _ = w.Write(png)
	}
}
