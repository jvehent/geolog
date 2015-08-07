GeoLog
======
Geolog is a Go program that detects potentially fraudulent accesses of user
accounts based on IP Geolocations.

It expects data in a very specific input format:
```
2014/09
  bob@example.net
      370 128.61.0.254
        9 116.170.50.90
  alice@example.com
     2271 198.19.212.229
      792 82.146.59.46
        1 193.243.46.194
```

For each month, GeoLog calculate the geocenter of activity of a given user. The
geocenter is placed where most connections originates from, by geolocating each
IP using MaxMind's database and ponderating that with the number of hits.

Once the geocenter is defined, GeoLog calculates the distance between each IP in
the monthly user set and the geocenter. If the distance is greater than 5,000km
(a configurable default), GeoLog prints an alert:

```
    in 2015/06, bob@example.net connected from United Kingdom 227 times; src ip 8.3.5.1
    was 7167km away from usual connection center in Kansas, USA
```

Run it!
-------

Make sure you have a copy of GeoIP2-City.mmdb locally and run:
```
$ go run geolog.go -i testsample.txt -m GeoIP2-City.mmdb
```

If the `-k` flag is given with a valid Google API key, GeoLog attempt to reverse
geocode the location of the geocenter. In the example above, the geocenter is in
Kansas, USA.

```
$ go run geolog.go -m GeoIP2-City.mmdb -i testsample.txt -k AIoqiefhqp98ohgwlafd2
```
