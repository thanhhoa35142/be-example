// Package crowd produces the crowd component of the score: the self-built
// estimate (base × hourly curve × weather factor × holiday factor, normalized
// per city), community reports, and a BestTime adapter for phase B. Sources
// are never blended silently — model/community/provider stay labeled.
package crowd
