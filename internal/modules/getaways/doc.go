// Package getaways answers "which covered city has the best picnic weather on
// the days I pick" (docs/GETAWAYS_SPEC.md, tier 1).
//
// Refresh (worker job getaways.refresh) condenses t_spot_scores into an hourly
// city weather series, t_city_hour_outlook: per pack and start hour 08-19
// local, the median over active spots of LEAST(s_weather, cap value). List
// (GET /v1/getaways) reads that series for the requested local days, keeps the
// hours from now on, finds each day's best 2-4 h window with
// digest.BestWindow and ranks the cities. The server takes no coordinates.
package getaways
