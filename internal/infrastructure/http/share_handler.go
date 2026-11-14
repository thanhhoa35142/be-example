package http

import (
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// ShareOptions configures the public share surface (not part of /v1):
//
//	GET /s/{spotId}                               share page for a spot
//	GET /.well-known/apple-app-site-association   universal-link association
//
// The app shares https://<share host>/s/<spotId>. With the app installed iOS
// opens it directly (universal link); everyone else gets this page.
type ShareOptions struct {
	// AppleAppIDs are "<TeamID>.<bundle id>" entries allowed to open /s/*
	// as universal links. Empty: the association file answers 404.
	AppleAppIDs []string
	// AppStoreAppID is the numeric App Store id; enables the "Get the app"
	// link and the Safari smart banner. Empty: both are left out.
	AppStoreAppID string
}

type ShareHandler struct {
	svc  *spots.Service
	opts ShareOptions
}

func NewShareHandler(svc *spots.Service, opts ShareOptions) *ShareHandler {
	return &ShareHandler{svc: svc, opts: opts}
}

type sharePage struct {
	Title       string
	Subtitle    string
	Description string
	HasScore    bool
	Score       int
	Tier        string
	AppURL      template.URL // pickhour://spot/<id>
	MapsURL     string
	StoreURL    string
	AppStoreID  string
}

// GET /s/:id
func (h *ShareHandler) Spot(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.notFound(c)
		return
	}
	detail, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		var merged spots.MergedError
		switch {
		case errors.As(err, &merged):
			c.Redirect(http.StatusMovedPermanently, "/s/"+merged.NewID.String())
		case errors.Is(err, spots.ErrNotFound):
			h.notFound(c)
		default:
			httpx.WriteError(c, err)
		}
		return
	}

	page := sharePage{
		Title:      shareTitle(detail),
		Subtitle:   shareSubtitle(detail),
		AppURL:     template.URL("pickhour://spot/" + id.String()),
		MapsURL:    mapsURL(shareTitle(detail), detail.Lat, detail.Lon),
		AppStoreID: h.opts.AppStoreAppID,
	}
	if h.opts.AppStoreAppID != "" {
		page.StoreURL = "https://apps.apple.com/app/id" + h.opts.AppStoreAppID
	}
	// Current-hour score; the page still renders without one.
	if items, err := h.svc.List(c.Request.Context(), spots.ListQuery{IDs: []uuid.UUID{id}}); err == nil &&
		len(items) == 1 && items[0].Score != nil && items[0].Score.Score != nil {
		page.HasScore = true
		page.Score = *items[0].Score.Score
		page.Tier = scoreTier(page.Score)
	}
	page.Description = "See the best hour for a picnic here, with an hourly score from 0 to 100."
	if page.HasScore {
		page.Description = "Picnic score right now: " + strconv.Itoa(page.Score) + " (" + page.Tier + "). See the best hour in Pickhour."
	}

	c.Header("Cache-Control", "public, max-age=300")
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := shareTemplate.Execute(c.Writer, page); err != nil {
		_ = c.Error(err)
	}
}

func (h *ShareHandler) notFound(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusNotFound, "<!doctype html><meta charset=utf-8><title>Pickhour</title><p>This spot is no longer available.</p>")
}

// GET /.well-known/apple-app-site-association — must be JSON over HTTPS with
// no redirect. Only /s/* is claimed.
func (h *ShareHandler) AppleAppSiteAssociation(c *gin.Context) {
	if len(h.opts.AppleAppIDs) == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	c.JSON(http.StatusOK, gin.H{
		"applinks": gin.H{
			"details": []gin.H{{
				"appIDs":     h.opts.AppleAppIDs,
				"components": []gin.H{{"/": "/s/*"}},
			}},
		},
	})
}

func shareTitle(d spots.SpotDetail) string {
	if d.Name != nil && strings.TrimSpace(*d.Name) != "" {
		return *d.Name
	}
	if d.City != nil && *d.City != "" {
		return kindLabel(d.Kind) + " · " + *d.City
	}
	return kindLabel(d.Kind)
}

func shareSubtitle(d spots.SpotDetail) string {
	named := d.Name != nil && strings.TrimSpace(*d.Name) != ""
	if named && d.City != nil && *d.City != "" {
		return kindLabel(d.Kind) + " · " + *d.City
	}
	if named {
		return kindLabel(d.Kind)
	}
	return ""
}

func kindLabel(k spots.Kind) string {
	switch string(k) {
	case "park":
		return "Park"
	case "beach":
		return "Beach"
	case "site":
		return "Picnic area"
	case "table":
		return "Picnic table"
	default:
		return "Spot"
	}
}

// scoreTier mirrors the app's tiers (80 / 60 / 40).
func scoreTier(score int) string {
	switch {
	case score >= 80:
		return "Great"
	case score >= 60:
		return "Good"
	case score >= 40:
		return "Fair"
	default:
		return "Poor"
	}
}

func mapsURL(name string, lat, lon float64) string {
	q := url.Values{}
	q.Set("ll", strconv.FormatFloat(lat, 'f', 6, 64)+","+strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("q", name)
	return "https://maps.apple.com/?" + q.Encode()
}

// Flat page: system font, neutral background, one brand color, no images.
var shareTemplate = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Pickhour</title>
<meta name="description" content="{{.Description}}">
<meta property="og:type" content="website">
<meta property="og:site_name" content="Pickhour">
<meta property="og:title" content="{{.Title}}">
<meta property="og:description" content="{{.Description}}">
<meta name="twitter:card" content="summary">
{{if .AppStoreID}}<meta name="apple-itunes-app" content="app-id={{.AppStoreID}}">{{end}}
<style>
:root { color-scheme: light dark; --brand: #2E7D4F; --bg: #F2F2F7; --surface: #FFFFFF; --text: #000000; --text2: #6C6C70; }
@media (prefers-color-scheme: dark) { :root { --brand: #5DC983; --bg: #000000; --surface: #1C1C1E; --text: #FFFFFF; --text2: #98989F; } }
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--text); font: 17px/1.4 -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
main { max-width: 460px; margin: 0 auto; padding: 32px 16px; }
.brand { color: var(--text2); font-size: 15px; margin: 0 0 16px; }
.panel { background: var(--surface); border-radius: 12px; padding: 20px; }
h1 { font-size: 28px; line-height: 1.2; margin: 0; }
.sub { color: var(--text2); margin: 4px 0 0; }
.score { margin: 20px 0 0; display: flex; align-items: baseline; gap: 8px; }
.score b { font-size: 44px; line-height: 1; font-variant-numeric: tabular-nums; }
.score span { color: var(--brand); font-weight: 600; }
.score small { color: var(--text2); font-size: 15px; margin-left: auto; }
.actions { margin-top: 20px; display: grid; gap: 10px; }
a.button { display: block; text-align: center; padding: 14px; border-radius: 12px; text-decoration: none; font-weight: 600; }
a.primary { background: var(--brand); color: var(--surface); }
a.secondary { color: var(--brand); background: transparent; border: 1px solid var(--brand); }
footer { color: var(--text2); font-size: 13px; margin-top: 20px; }
footer a { color: inherit; }
</style>
</head>
<body>
<main>
<p class="brand">Pickhour</p>
<div class="panel">
<h1>{{.Title}}</h1>
{{if .Subtitle}}<p class="sub">{{.Subtitle}}</p>{{end}}
{{if .HasScore}}<p class="score"><b>{{.Score}}</b><span>{{.Tier}}</span><small>Picnic score right now</small></p>{{end}}
<div class="actions">
<a class="button primary" href="{{.AppURL}}">Open in Pickhour</a>
{{if .StoreURL}}<a class="button secondary" href="{{.StoreURL}}">Get the App</a>{{end}}
<a class="button secondary" href="{{.MapsURL}}">Directions</a>
</div>
</div>
<footer>Map data © <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors.</footer>
</main>
</body>
</html>
`))
