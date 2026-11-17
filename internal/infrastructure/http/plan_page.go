package http

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/plans"
	"github.com/thanhhoa35142/be-example/internal/modules/spots"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// PlanPage serves GET /p/:token, the public page of a shared plan: place,
// local time window, the live window score and state, at most 3 reasons.
// Nothing about the owner or followers, no alternative; noindex and only
// privately cached for 60 s (FEATURE_PLAN_DECISION_PLANS_GRASS.md C2).
func (h *ShareHandler) PlanPage(c *gin.Context) {
	c.Header("Cache-Control", "private, max-age=60")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Referrer-Policy", "no-referrer")
	if h.opts.Plans == nil {
		h.planNotFound(c)
		return
	}
	token := c.Param("token")
	sh, err := h.opts.Plans.Shared(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, plans.ErrNotFound) {
			h.planNotFound(c)
			return
		}
		httpx.WriteError(c, err)
		return
	}
	page := planPage(*sh, token, h.opts.AppStoreAppID)
	c.Status(http.StatusOK)
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := shareTemplate.Execute(c.Writer, page); err != nil {
		_ = c.Error(err)
	}
}

func planPage(sh plans.Shared, token, appStoreID string) sharePage {
	kind := spots.Kind(sh.SpotKind)
	title := kindLabel(kind)
	sub := ""
	switch name := strings.TrimSpace(sh.SpotName); {
	case name != "":
		title = name
		sub = kindLabel(kind)
		if sh.SpotCity != "" {
			sub += " · " + sh.SpotCity
		}
	case sh.SpotCity != "":
		title += " · " + sh.SpotCity
	}
	loc := time.UTC
	if l, err := time.LoadLocation(sh.TZ); err == nil {
		loc = l
	}
	start, end := sh.Start.In(loc), sh.End.In(loc)
	window := start.Format("15:04") + "–" + end.Format("15:04")
	page := sharePage{
		Title:      title,
		Subtitle:   sub,
		NoIndex:    true,
		HasAt:      true,
		AtLabel:    start.Format("Monday 2 January") + " · " + window,
		ScoreLabel: "Picnic score for this plan",
		// The token is checked against ^[A-Za-z0-9_-]{22}$ before any
		// lookup, so it is safe in a URL as is.
		AppURL:     template.URL("pickhour://plan/" + token),
		MapsURL:    mapsURL(title, sh.Lat, sh.Lon),
		AppStoreID: appStoreID,
		Canonical:  "/p/" + token,
	}
	if appStoreID != "" {
		page.StoreURL = "https://apps.apple.com/app/id" + appStoreID
	}
	page.OGTitle = "Picnic on " + start.Format("Monday 2 Jan") + ", " + window + " · " + title
	when := start.Format("Monday 2 January") + ", " + window
	if !sh.HasWindow {
		page.Description = "Picnic planned for " + when + ". See the hourly forecast in Pickhour."
		return page
	}
	page.HasScore = true
	page.Score = sh.Window.Mean
	page.Tier = scoreTier(page.Score)
	for _, r := range sh.Window.Worst.Reasons {
		if s := reasonText(r); s != "" {
			page.Reasons = append(page.Reasons, s)
		}
		if len(page.Reasons) == maxSharedReasons {
			break
		}
	}
	page.OpenHint = sh.State != nil && *sh.State != plans.StateOK
	page.Description = "Picnic planned for " + when + ". Score for this plan: " +
		strconv.Itoa(page.Score) + " (" + page.Tier + ")."
	if len(page.Reasons) > 0 {
		page.Description += " " + strings.Join(page.Reasons, " · ") + "."
	}
	return page
}

// planNotFound is the one response for unknown, malformed, canceled and
// ended tokens.
func (h *ShareHandler) planNotFound(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusNotFound, `<!doctype html><meta charset=utf-8><meta name="robots" content="noindex"><title>Pickhour</title><p>This plan is no longer available.</p>`)
}
