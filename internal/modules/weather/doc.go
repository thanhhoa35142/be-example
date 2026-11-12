// Package weather fetches forecasts per H3 grid cell from WeatherKit (or a
// compatible provider), stores them in weather_hourly, and serves the
// 14-day rolling cache used by scoring. Refresh target: hourly.
package weather
