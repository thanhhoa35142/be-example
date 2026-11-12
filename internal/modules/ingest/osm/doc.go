// Package osm implements .pbf import from Geofabrik extracts: clustering OSM
// objects into spots, assigning stable UUIDv7 IDs, computing amenities via
// ST_DWithin, and swapping staged data into live tables atomically.
package osm
