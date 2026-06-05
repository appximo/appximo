package observability

import (
	_ "embed"
	"net"

	maxminddb "github.com/oschwald/maxminddb-golang"
)

// geoDB is the embedded MaxMind GeoLite2-Country database (no network, no API).
//
//go:embed assets/GeoLite2-Country.mmdb
var geoDB []byte

// GeoLookup resolves an IP to an ISO country code using an in-memory GeoLite2
// database. Lookups are ~1µs and never touch the network. A GeoLookup whose
// database failed to load returns "" for everything — graceful degradation, so a
// missing/corrupt mmdb never blocks startup or panics.
type GeoLookup struct {
	db *maxminddb.Reader
}

// NewGeoLookup builds a lookup from a GeoLite2-Country mmdb buffer. An empty or
// invalid buffer yields a usable no-op GeoLookup (Country → "") rather than an
// error.
func NewGeoLookup(data []byte) *GeoLookup {
	if len(data) == 0 {
		return &GeoLookup{}
	}
	db, err := maxminddb.FromBytes(data)
	if err != nil {
		return &GeoLookup{}
	}
	return &GeoLookup{db: db}
}

// DefaultGeoLookup builds a lookup from the embedded GeoLite2 database.
func DefaultGeoLookup() *GeoLookup { return NewGeoLookup(geoDB) }

// Country returns the ISO-3166 country code for ip ("CO", "US", "MX", …), or ""
// when the database is unavailable, the ip is invalid/private/loopback, or no
// country is recorded.
func (g *GeoLookup) Country(ip string) string {
	if g == nil || g.db == nil || ip == "" {
		return ""
	}
	pip := net.ParseIP(ip)
	if pip == nil {
		return ""
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
	}
	if err := g.db.Lookup(pip, &rec); err != nil {
		return ""
	}
	return rec.Country.ISOCode
}
