// Julien Vehent [:ulfr] - jvehent@mozilla.com

package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	geo "github.com/oschwald/geoip2-golang"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"
)

type Traveler struct {
	ID            string     `json:"id"`
	Geocenter     Geocenter  `json:"geocenter,omitempty"`
	Locations     []Location `json:"locations,omitempty"`
	Alerts        []string   `json:"alerts"`
	AlertDistance float64    `json:"alert_dist"`
}

type Geocenter struct {
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Weight    float64 `json:"weight,omitempty"`
	Locality  string  `json:"locality,omitempty"`
	AvgDist   float64 `json:"avg_dist, omitempty"`
}

type Location struct {
	IP        string    `json:"ip,omitempty"`
	Date      time.Time `json:"date,omitempty"`
	Latitude  float64   `json:"latitude,omitempty"`
	Longitude float64   `json:"longitude,omitempty"`
	Weight    float64   `json:"weight,omitempty"`
	Locality  string    `json:"locality,omitempty"`
}

func main() {
	var maxdb = flag.String("m", "GeoIP2-City.mmdb", "Location of Maxmind database")
	var src = flag.String("i", "logfile.txt", "Source log file")
	var geoDistThreshold = flag.Float64("d", 5000.0, "Distance an IP must be from the geocenter to alert")
	var googleKey = flag.String("k", "", "Key to Google geocoding API")
	var mapsPath = flag.String("maps", "", "Create maps under the path given as argument. Maps are labelled <path>/<traveler id>.html")
	flag.Parse()

	maxmind, err := geo.Open(*maxdb)
	if err != nil {
		panic(err)
	}

	travelers, err := parse_travelers_logs(*src, maxmind)
	if err != nil {
		panic(err)
	}

	for email := range travelers {
		fmt.Println("Evaluating traveler", email)
		tvl := travelers[email]
		tvl.AlertDistance = *geoDistThreshold
		tvl.Geocenter, err = find_geocenter(tvl, *googleKey)
		if err != nil {
			panic(err)
		}
		for _, loc := range tvl.Locations {
			geodist := km_between_two_points(loc.Latitude, loc.Longitude, tvl.Geocenter.Latitude, tvl.Geocenter.Longitude)
			if geodist > tvl.AlertDistance {
				alert := fmt.Sprintf("in %s, %s connected from %s %.0f times; srcip='%s' (%.6f,%.6f) was %.0fkm away from usual connection center ",
					loc.Date.Format("2006/01"), email, loc.Locality, loc.Weight, loc.IP, loc.Latitude, loc.Longitude, geodist)
				if tvl.Geocenter.Locality != "" {
					alert += fmt.Sprintf("in %s ", tvl.Geocenter.Locality)
				}
				alert += fmt.Sprintf("(%.6f,%.6f)\n", tvl.Geocenter.Latitude, tvl.Geocenter.Longitude)
				fmt.Println(alert)
				tvl.Alerts = append(tvl.Alerts, alert)
			}
		}
		if *mapsPath != "" {
			make_traveler_map(tvl, *mapsPath)
		}
	}
}

func parse_travelers_logs(src string, maxmind *geo.Reader) (travelers map[string]Traveler, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("parse_travelers_logs() -> %v", e)
		}
	}()
	travelers = make(map[string]Traveler)

	date := regexp.MustCompile(`^(201[1-5]/(0|1)[0-9])$`)
	email := regexp.MustCompile(`^  (\S.+@.+)$`)
	hits := regexp.MustCompile(`\s+([0-9]{1,10})\s([0-9].+)$`)
	fd, err := os.Open(src)
	if err != nil {
		panic(err)
	}
	defer fd.Close()
	scanner := bufio.NewScanner(fd)
	var (
		cdate       time.Time
		cemail, cip string
		chits       float64
	)
	for scanner.Scan() {
		if err := scanner.Err(); err != nil {
			panic(err)
		}
		if date.MatchString(scanner.Text()) {
			fields := date.FindAllStringSubmatch(scanner.Text(), -1)
			cdate, err = time.Parse("2006/01", fields[0][1])
			if err != nil {
				panic(err)
			}
		} else if email.MatchString(scanner.Text()) {
			fields := email.FindAllStringSubmatch(scanner.Text(), -1)
			cemail = fields[0][1]
		} else if hits.MatchString(scanner.Text()) {
			fields := hits.FindAllStringSubmatch(scanner.Text(), -1)
			chits, err = strconv.ParseFloat(fields[0][1], 64)
			if err != nil {
				panic(err)
			}
			cip = fields[0][2]
			record, err := maxmind.City(net.ParseIP(cip))
			if err != nil {
				panic(err)
			}

			var cloc = Location{
				IP:        cip,
				Date:      cdate,
				Weight:    chits,
				Latitude:  record.Location.Latitude,
				Longitude: record.Location.Longitude,
				Locality:  record.City.Names["en"] + ", " + record.Country.Names["en"],
			}
			var tvl Traveler
			if _, ok := travelers[cemail]; !ok {
				tvl.Locations = append(tvl.Locations, cloc)
				tvl.ID = cemail
			} else {
				tvl = travelers[cemail]
				tvl.Locations = append(tvl.Locations, cloc)
			}
			travelers[cemail] = tvl
		}
	}
	return
}

func find_geocenter(tvl Traveler, gk string) (gc Geocenter, err error) {
	var lat, lon_gw, lon_dl float64
	// First pass: calculate two geocenters: one on the greenwich meridian
	// and one of the dateline meridian
	for _, loc := range tvl.Locations {
		lat += (loc.Latitude * loc.Weight)
		lon_gw += (loc.Longitude * loc.Weight)
		lon_dl += (switch_meridians(loc.Longitude) * loc.Weight)
		gc.Weight += loc.Weight
	}
	lat /= gc.Weight
	lon_gw /= gc.Weight
	lon_dl /= gc.Weight
	lon_dl = switch_meridians(lon_dl)

	// Second pass: calculate the distance of each location to the greenwich
	// meridian and the dateline meridian. The average distance that is the
	// shortest indicates which meridian is appropriate to use.
	var dist_to_gw, avg_dist_to_gw, dist_to_dl, avg_dist_to_dl float64
	for _, loc := range tvl.Locations {
		dist_to_gw = km_between_two_points(loc.Latitude, loc.Longitude, lat, lon_gw)
		avg_dist_to_gw += (dist_to_gw * loc.Weight)
		dist_to_dl = km_between_two_points(loc.Latitude, loc.Longitude, lat, lon_dl)
		avg_dist_to_dl += (dist_to_dl * loc.Weight)
	}
	avg_dist_to_gw /= gc.Weight
	avg_dist_to_dl /= gc.Weight
	if avg_dist_to_gw > avg_dist_to_dl {
		// average distance to greenwich meridian is longer than average distance
		// to dateline meridian, so the dateline meridian is our geocenter
		gc.Longitude = lon_dl
		gc.AvgDist = avg_dist_to_dl
	} else {
		gc.Longitude = lon_gw
		gc.AvgDist = avg_dist_to_gw
	}
	gc.Latitude = lat
	if gk != "" {
		gc.Locality, err = reverse_geocode(gc.Latitude, gc.Longitude, gk)
	}
	return
}

// haversin(θ) function
func hsin(theta float64) float64 {
	return math.Pow(math.Sin(theta/2), 2)
}

// Distance function returns the distance (in meters) between two points of
//     a given longitude and latitude relatively accurately (using a spherical
//     approximation of the Earth) through the Haversin Distance Formula for
//     great arc distance on a sphere with accuracy for small distances
//
// point coordinates are supplied in degrees and converted into rad. in the func
//
// distance returned is Kilometers
// http://en.wikipedia.org/wiki/Haversine_formula
func km_between_two_points(lat1, lon1, lat2, lon2 float64) float64 {
	// convert to radians
	// must cast radius as float to multiply later
	var la1, lo1, la2, lo2, r float64
	la1 = lat1 * math.Pi / 180
	lo1 = lon1 * math.Pi / 180
	la2 = lat2 * math.Pi / 180
	lo2 = lon2 * math.Pi / 180

	r = 6378 // Earth radius in Kilometers

	// calculate
	h := hsin(la2-la1) + math.Cos(la1)*math.Cos(la2)*hsin(lo2-lo1)

	return 2 * r * math.Asin(math.Sqrt(h))
}

func switch_meridians(lon float64) float64 {
	if lon < 0.0 {
		return lon + 180.0
	}
	return lon - 180.0
}

type GoogleGeocode struct {
	Results []struct {
		FormattedAddress string   `json:"formatted_address"`
		Types            []string `json:"types"`
	} `json:"results"`
}

func reverse_geocode(lat, lon float64, key string) (location string, err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("reverse_geocode() -> %v", e)
		}
	}()
	time.Sleep(222 * time.Millisecond) // throttled at 5qps in the free api
	query := fmt.Sprintf("https://maps.googleapis.com/maps/api/geocode/json?latlng=%.6f,%.6f&key=%s", lat, lon, key)
	resp, err := http.Get(query)
	defer resp.Body.Close()
	if err != nil {
		panic(fmt.Sprintf("failed to retrieve location from google geocoding api: %v", err))
	}
	if resp.StatusCode != 200 {
		panic("Google APIs call failed with code " + resp.Status)
	}
	var geocode GoogleGeocode
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		panic(fmt.Sprintf("failed to read response from Google geocoding api: %v", err))
	}
	err = json.Unmarshal(body, &geocode)
	if err != nil {
		panic(fmt.Sprintf("invalid JSON response body from Google geocoding api: %v", err))
	}
	for _, r := range geocode.Results {
		for _, t := range r.Types {
			if t == "locality" || t == "administrative_area_level_1" {
				return r.FormattedAddress, nil
			}
		}
	}
	return "", nil
}

func make_traveler_map(tvl Traveler, path string) (err error) {
	defer func() {
		if e := recover(); e != nil {
			err = fmt.Errorf("PrintMap() -> %v", e)
		}
	}()
	gmap := makeMapHeader("Travel map of " + tvl.ID)
	tvl.Locations = singularizeLocations(tvl.Locations)
	data, err := json.Marshal(tvl)
	if err != nil {
		panic(err)
	}
	gmap += fmt.Sprintf(`        <script type="text/javascript"> var traveler = %s </script>`, data)
	var details []string
	details = append(details, fmt.Sprintf("        <p>Traveler map of %s. Geocenter in %s.</p>", tvl.ID, tvl.Geocenter.Locality))
	details = append(details, "        <ol>\n")
	for _, alert := range tvl.Alerts {
		details = append(details, fmt.Sprintf("            <li>%s</li>", alert))
	}
	details = append(details, "        </ol>\n")
	gmap += makeMapFooter("Travel map of "+tvl.ID, details)

	// write map data to temp file
	fd, err := os.Create(path + "/" + tvl.ID + ".html")
	defer fd.Close()
	if err != nil {
		panic(err)
	}
	_, err = fd.Write([]byte(gmap))
	if err != nil {
		panic(err)
	}
	fi, err := fd.Stat()
	if err != nil {
		panic(err)
	}
	filepath := fmt.Sprintf("%s/%s", os.TempDir(), fi.Name())
	fmt.Fprintf(os.Stderr, "map written to %s\n", filepath)
	return
}

// singularizeLocations prevent multiple point from using the same coordinates, and therefore show as one point on the map
func singularizeLocations(orig_locs []Location) (locs []Location) {
	locs = orig_locs
	for i, _ := range locs {
		for j := 0; j < i; j++ {
			if locs[i].Latitude == locs[j].Latitude && locs[i].Longitude == locs[j].Longitude {
				switch i % 8 {
				case 0:
					locs[i].Latitude += 0.005
				case 1:
					locs[i].Longitude += 0.005
				case 2:
					locs[i].Latitude -= 0.005
				case 3:
					locs[i].Longitude -= 0.005
				case 4:
					locs[i].Latitude += 0.005
					locs[i].Longitude += 0.005
				case 5:
					locs[i].Latitude -= 0.005
					locs[i].Longitude -= 0.005
				case 6:
					locs[i].Latitude += 0.005
					locs[i].Longitude -= 0.005
				case 7:
					locs[i].Latitude -= 0.005
					locs[i].Longitude += 0.005
				}
			}
		}
	}
	return
}

func makeMapHeader(title string) string {
	return fmt.Sprintf(`
<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Strict//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-strict.dtd">
<html xmlns="http://www.w3.org/1999/xhtml" xml:lang="en" lang="en">
    <head>
        <meta http-equiv="Content-Type" content="text/html;charset=utf-8" />
        <title>%s</title>
        <script type="text/javascript" src="https://maps.googleapis.com/maps/api/js?v=3.exp&amp;signed_in=true"></script>
        <script type="text/javascript" src="https://raw.githubusercontent.com/googlemaps/js-marker-clusterer/gh-pages/src/markerclusterer_compiled.js"></script>
`, title)
}

func makeMapFooter(title string, body []string) (footer string) {
	footer = `
		<script type="text/javascript">
var locs = new Array();
var marker = new Array();
var connections = new Array();
var cluster = new Array();
var arrowSymbol = {
	path: google.maps.SymbolPath.CIRCLE,
	scale: 2,
	strokeColor: 'blue'
};
function initialize() {
	var center = new google.maps.LatLng(traveler.geocenter.latitude, traveler.geocenter.longitude);
	var mapOptions = {
		zoom: 3,
		center: center,
		mapTypeId: google.maps.MapTypeId.TERRAIN
	};
	var map = new google.maps.Map(
		document.getElementById('map'),
		mapOptions
	);

	var avg_conn_radius = {
		strokeColor: '#26b301',
		strokeOpacity: 0.8,
		strokeWeight: 2,
		fillColor: '#97e881',
		fillOpacity: 0.2,
		map: map,
		center: center,
		radius: traveler.geocenter.avg_dist * 1000
	};
	connradius = new google.maps.Circle(avg_conn_radius);

	var alert_radius = {
		strokeColor: '#f1001c',
		strokeOpacity: 0.8,
		strokeWeight: 2,
		fillColor: '#f1001c',
		fillOpacity: 0.05,
		map: map,
		center: center,
		radius: traveler.alert_dist * 1000
	};
	alertradius = new google.maps.Circle(alert_radius);

	locationscount = traveler.locations.length;
	for (var i=0; i<locationscount; i++) {
		locs[traveler.locations[i].ip] = new google.maps.LatLng(traveler.locations[i].latitude, traveler.locations[i].longitude);
		marker[traveler.locations[i].ip] = new google.maps.Marker({
			position: locs[traveler.locations[i].ip],
			map: map,
			title: traveler.locations[i].date + ": " + traveler.locations[i].locality + ", " + traveler.locations[i].ip + ", " + traveler.locations[i].weight + " hits"
		});
		cluster.push(marker[traveler.locations[i].ip]);
	}
	for (var i=0; i<locationscount; i++) {
		connections[i] = new google.maps.Polyline({
		path: [locs[traveler.locations[i].ip], center],
		geodesic: true,
		strokeColor: 'blue',
		strokeOpacity: 1.0,
		strokeWeight: 1,
		icons: [{
			icon: arrowSymbol,
			offset: '100%'
		}],
		map: map
		});
	}
	var markerCluster = new MarkerClusterer(map, cluster);
}
google.maps.event.addDomListener(window, 'load', initialize);
		</script>
		<style type="text/css">
			#map {
				width:100%;
				height:900px;
			}
		</style>
	</head>
	<body>
`
	footer += fmt.Sprintf("        <p><b>%s</b></p>\n", title)
	footer += `<div id="map"></div>`
	for _, p := range body {
		footer += p + "\n"
	}
	footer += `
    </body>
</html>`
	return
}
