package http

import (
	"errors"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

type ShareOptions struct {
	AppleAppIDs []string

	AppStoreAppID string

	// Plans backs /p/:token; nil serves 404 there.
	Plans *plans.Service
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
	OGTitle     string
	Subtitle    string
	Description string
	HasScore    bool
	Score       int
	Tier        string
	ScoreLabel  string
	AppURL      template.URL
	MapsURL     string
	StoreURL    string
	AppStoreID  string

	// Planned-time variant (`?at=`): one hour of one spot, nothing else.
	NoIndex   bool
	HasAt     bool
	AtLabel   string
	Reasons   []string
	OpenHint  bool
	Canonical string
}

// shareAtMaxLen bounds the raw `at` value; a valid RFC3339 timestamp with
// nanoseconds and an offset is 35 bytes.
const shareAtMaxLen = 40

// shareAtLayout is the one canonical `at` form: the UTC hour, no fraction.
const shareAtLayout = "2006-01-02T15:04:05Z"

// shareAtHorizon matches the scoring horizon (7 days of hourly scores).
const shareAtHorizon = spots.ForecastDays * 24 * time.Hour

// parseShareAt validates the `at` query value. ok is false for anything that
// is not RFC3339, is before the current UTC hour, or is at/after the scoring
// horizon. The result is truncated to the hour; canonical reports whether raw
// already is the canonical string for that hour.
func parseShareAt(raw string, now time.Time) (at time.Time, canonical, ok bool) {
	if raw == "" || len(raw) > shareAtMaxLen {
		return time.Time{}, false, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, false
	}
	at = t.UTC().Truncate(time.Hour)
	nowHour := now.UTC().Truncate(time.Hour)
	if at.Before(nowHour) || !at.Before(nowHour.Add(shareAtHorizon)) {
		return time.Time{}, false, false
	}
	return at, raw == at.Format(shareAtLayout), true
}

func shareURL(id uuid.UUID, at *time.Time) string {
	u := "/s/" + id.String()
	if at != nil {
		u += "?" + url.Values{"at": {at.Format(shareAtLayout)}}.Encode()
	}
	return u
}

func (h *ShareHandler) Spot(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		h.notFound(c)
		return
	}

	rawAt, hasAtParam := c.GetQuery("at")
	var at *time.Time
	if hasAtParam {
		if t, canonical, ok := parseShareAt(rawAt, time.Now()); ok {
			if !canonical {
				// One URL per spot-hour, so shared caches and link previews
				// never see more than ~168 variants per spot. The redirect
				// itself stays out of shared caches: its URL space (any
				// minute, second, fraction or offset) is unbounded.
				c.Header("Cache-Control", "private, max-age=300")
				c.Redirect(http.StatusFound, shareURL(id, &t))
				return
			}
			at = &t
		}
	}

	detail, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		var merged spots.MergedError
		switch {
		case errors.As(err, &merged):
			c.Redirect(http.StatusMovedPermanently, shareURL(merged.NewID, at))
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
		ScoreLabel: "Picnic score right now",
		NoIndex:    hasAtParam,
	}
	page.OGTitle = page.Title
	if h.opts.AppStoreAppID != "" {
		page.StoreURL = "https://apps.apple.com/app/id" + h.opts.AppStoreAppID
	}

	q := spots.ListQuery{IDs: []uuid.UUID{id}}
	if at != nil {
		q.When = at
	}
	var snap *spots.ScoreSnapshot
	if items, err := h.svc.List(c.Request.Context(), q); err == nil &&
		len(items) == 1 && items[0].Score != nil && items[0].Score.Score != nil {
		snap = items[0].Score
		page.HasScore = true
		page.Score = *snap.Score
		page.Tier = scoreTier(page.Score)
	}

	if at != nil {
		fillPlannedPage(&page, detail, *at, snap)
		page.Canonical = shareURL(id, at)
		// Only the uuid and our own formatted time go into this URL.
		page.AppURL = template.URL("pickhour://spot/" + id.String() + "?" +
			url.Values{"at": {at.Format(shareAtLayout)}}.Encode())
	} else {
		page.Description = "See the best hour for a picnic here, with an hourly score from 0 to 100."
		if page.HasScore {
			page.Description = "Picnic score right now: " + strconv.Itoa(page.Score) + " (" + page.Tier + "). See the best hour in Pickhour."
		}
	}

	switch {
	case hasAtParam && at == nil:
		// Junk, past or out-of-horizon `at`: plain page, but keep these
		// variants out of shared caches.
		c.Header("Cache-Control", "private, max-age=300")
	default:
		c.Header("Cache-Control", "public, max-age=300")
	}
	if page.NoIndex {
		c.Header("X-Robots-Tag", "noindex")
	}
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
				"components": []gin.H{{"/": "/s/*"}, {"/": "/p/*"}},
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

// fillPlannedPage renders the `?at=` variant: the planned hour in the spot's
// timezone, that hour's score and up to 3 reasons. No other hours, days or
// alternatives (FEATURE_PLAN_DECISION_PLANS_GRASS.md, decision 2).
func fillPlannedPage(page *sharePage, d spots.SpotDetail, at time.Time, snap *spots.ScoreSnapshot) {
	loc := time.UTC
	if l, err := time.LoadLocation(d.TZ); err == nil && d.TZ != "" {
		loc = l
	}
	local := at.In(loc)
	page.HasAt = true
	page.AtLabel = local.Format("Monday 2 January · 15:04")
	page.ScoreLabel = "Picnic score at " + local.Format("15:04")
	page.OGTitle = "Picnic on " + local.Format("Monday 2 Jan, 15:04") + " · " + page.Title

	when := local.Format("Monday 2 January at 15:04")
	if !page.HasScore {
		page.Description = "Picnic planned for " + when + ". See the hourly forecast in Pickhour."
		return
	}
	if snap != nil {
		for _, r := range snap.Reasons {
			if s := reasonText(r); s != "" {
				page.Reasons = append(page.Reasons, s)
			}
			if len(page.Reasons) == 3 {
				break
			}
		}
	}
	page.OpenHint = page.Score < 60 || (snap != nil && snap.CapReason != nil)
	page.Description = "Picnic planned for " + when + ". Score for that hour: " +
		strconv.Itoa(page.Score) + " (" + page.Tier + ")."
	if len(page.Reasons) > 0 {
		page.Description += " " + strings.Join(page.Reasons, " · ") + "."
	}
}

// reasonText is the English text of a scoring reason code (the codes from
// scoring.topReasons). The API itself only returns codes; this static page is
// English-only, like the rest of its copy. Unknown codes render nothing.
func reasonText(r spots.Reason) string {
	num := func(k string) (string, bool) {
		switch v := r.Params[k].(type) {
		case float64:
			return strconv.FormatFloat(math.Round(v), 'f', 0, 64), true
		case int:
			return strconv.Itoa(v), true
		}
		return "", false
	}
	switch r.Code {
	case "cap.rain":
		return "Rain expected"
	case "cap.heat":
		return "Too hot"
	case "cap.cold":
		return "Too cold"
	case "cap.dark":
		return "After dark"
	case "cap.twilight":
		return "Low light"
	case "weather.rain_risk":
		if p, ok := num("prob"); ok {
			return p + "% chance of rain"
		}
		return "Chance of rain"
	case "weather.hot":
		if t, ok := num("temp"); ok {
			return "Hot, " + t + "°C"
		}
		return "Hot"
	case "weather.cold":
		if t, ok := num("temp"); ok {
			return "Cold, " + t + "°C"
		}
		return "Cold"
	case "weather.clear":
		if t, ok := num("temp"); ok {
			return "Pleasant, " + t + "°C"
		}
		return "Pleasant weather"
	case "crowd.low":
		return "Quiet"
	case "crowd.moderate":
		return "Moderately busy"
	case "crowd.high":
		return "Busy"
	case "amenity.well_equipped":
		return "Well equipped"
	case "fresh.recent":
		return "Recently verified"
	}
	return ""
}

func mapsURL(name string, lat, lon float64) string {
	q := url.Values{}
	q.Set("ll", strconv.FormatFloat(lat, 'f', 6, 64)+","+strconv.FormatFloat(lon, 'f', 6, 64))
	q.Set("q", name)
	return "https://maps.apple.com/?" + q.Encode()
}

var shareTemplate = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · Pickhour</title>
<meta name="description" content="{{.Description}}">
{{if .NoIndex}}<meta name="robots" content="noindex">{{end}}
{{if .Canonical}}<link rel="canonical" href="{{.Canonical}}">{{end}}
<meta property="og:type" content="website">
<meta property="og:site_name" content="Pickhour">
<meta property="og:title" content="{{.OGTitle}}">
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
.when { font-weight: 600; margin: 16px 0 0; }
ul.reasons { margin: 12px 0 0; padding-left: 20px; }
.hint { color: var(--text2); margin: 12px 0 0; }
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
{{if .HasAt}}<p class="when">{{.AtLabel}}</p>{{end}}
{{if .HasScore}}<p class="score"><b>{{.Score}}</b><span>{{.Tier}}</span><small>{{.ScoreLabel}}</small></p>{{end}}
{{if .Reasons}}<ul class="reasons">{{range .Reasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .OpenHint}}<p class="hint">Open Pickhour to see better times.</p>{{end}}
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
