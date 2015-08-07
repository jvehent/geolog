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

func main() {
	var maxdb = flag.String("m", "GeoIP2-City.mmdb", "Location of Maxmind database")
	var src = flag.String("i", "logfile.txt", "Source log file")
	var geoDistThreshold = flag.Float64("d", 5000.0, "Distance an IP must be from the geocenter to alert")
	var googlekey = flag.String("k", "someapikey", "Key to Google geocoding API")
	flag.Parse()

	maxmind, err := geo.Open(*maxdb)
	if err != nil {
		panic(err)
	}

	mm := make(map[string]map[string]map[string]float64)

	date := regexp.MustCompile(`^(201[1-5]/(0|1)[0-9])$`)
	email := regexp.MustCompile(`^  ([0-9a-zA-Z].+)$`)
	hits := regexp.MustCompile(`\s+([0-9]{1,10})\s([0-9].+)$`)
	fd, err := os.Open(*src)
	if err != nil {
		panic(err)
	}
	defer fd.Close()
	scanner := bufio.NewScanner(fd)
	var cmonth, cemail, cip string
	var chits float64
	for scanner.Scan() {
		if err := scanner.Err(); err != nil {
			panic(err)
		}
		if date.MatchString(scanner.Text()) {
			fields := date.FindAllStringSubmatch(scanner.Text(), -1)
			cmonth = fields[0][1]
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
			if _, ok := mm[cmonth]; !ok {
				mm[cmonth] = map[string]map[string]float64{
					cemail: map[string]float64{
						cip: chits,
					},
				}
			} else if _, ok := mm[cmonth][cemail]; !ok {
				mm[cmonth][cemail] = map[string]float64{
					cip: chits,
				}
			} else {
				mm[cmonth][cemail][cip] = chits
			}
		}
	}
	for month := range mm {
		for email := range mm[month] {
			gclat, gclon := find_geocenter(mm[month][email], maxmind)
			var geoLocation string
			if *googlekey != "someapikey" {
				geoLocation, err = reverse_geocode(gclat, gclon, *googlekey)
				if err != nil {
					panic(err)
				}
			}
			for ip, hits := range mm[month][email] {
				record, err := maxmind.City(net.ParseIP(ip))
				if err != nil {
					panic(err)
				}
				geodist := km_between_two_points(
					record.Location.Latitude, record.Location.Longitude,
					gclat, gclon)
				if geodist > *geoDistThreshold {
					fmt.Printf("in %s, %s connected from %s %s %.0f times; src ip %s was %.0fkm away from usual connection center",
						month, email, record.City.Names["en"], record.Country.Names["en"], hits, ip, geodist)
					if geoLocation != "" {
						fmt.Printf(" in %s", geoLocation)
					}
					fmt.Printf("\n")
				}
			}
		}
	}
}

func find_geocenter(records map[string]float64, maxmind *geo.Reader) (lat, lon float64) {
	var tot float64
	for ip, hits := range records {
		record, err := maxmind.City(net.ParseIP(ip))
		if err != nil {
			panic(err)
		}
		lat += (record.Location.Latitude * hits)
		lon += (record.Location.Longitude * hits)
		tot += hits
	}
	lat /= tot
	lon /= tot
	return
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
