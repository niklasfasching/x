package geo

import "math"

var earthRadiusKM float64 = 6371

// http://www.movable-type.co.uk/scripts/latlong.html
func Haversine(latA, lngA, latB, lngB float64) (km float64) {
	latA, lngA = latA*math.Pi/180, lngA*math.Pi/180
	latB, lngB = latB*math.Pi/180, lngB*math.Pi/180
	dLat, dLng := latB-latA, lngB-lngA
	a := math.Pow(math.Sin(dLat/2), 2) + math.Cos(latA)*math.Cos(latB)*math.Pow(math.Sin(dLng/2), 2)
	return 2 * math.Asin(math.Sqrt(a)) * earthRadiusKM
}

// http://www.movable-type.co.uk/scripts/latlong.html
func Offset(lat, lng, bearing, km float64) (float64, float64) {
	d, lat, lng, bearing := km/6371, lat*math.Pi/180, lng*math.Pi/180, bearing*math.Pi/180
	lat2 := math.Asin(math.Sin(lat)*math.Cos(d) + math.Cos(lat)*math.Sin(d)*math.Cos(bearing))
	lng2 := lng + math.Atan2(math.Sin(bearing)*math.Sin(d)*math.Cos(lat),
		math.Cos(d)-math.Sin(lat)*math.Sin(lat2))
	return lat2 * 180 / math.Pi, lng2 * 180 / math.Pi
}
