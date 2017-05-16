## Installation

With GOPATH defined, run:

```
$ go get github.com/dsoprea/go-napster-to-spotify-sync/napster-to-spotify-sync
```

To run:

```
$ $GOPATH/bin/napster-to-spotify-sync \
--napster-api-key <NAPSTER API KEY> \
--napster-secret-key <NAPSTER API SECRET> \
--spotify-api-client-id <SPOTIFY API CLIENT-ID> \
--spotify-api-secret-key <SPOTIFY API SECRET-KEY> \
--napster-username <NAPSTER USERNAME/EMAIL ADDRESS> \
--napster-password <NAPSTER PASSWORD> \
--only-artists <ARTIST NAME> \
--spotify-album-market <2-letter country-code, e.g. US> \
-p <PLAYLIST NAME>
```


## Notes

- The album searches will often return duplicate results because the album has been released separately for different markets. Though we will only use thefirst, it is recommended that you provide the market-name to ensure that we will use the right one.

- Our assumption is that you don't want to push all of your favorited tracks in Napster to the same playlist in Spotify. So, you are required to pass one or more "--only-artists" arguments. The tool will print the artists that were skipped:

```
...
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [alter bridge]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [american authors]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [amorphis]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [andrea bocelli]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [anointed]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [apocalyptica]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [audioslave]
2017/05/16 09:30:55 gnss.import: [WARNING]  IGNORING ARTIST: [avenged sevenfold]
...
```


## Command-Line Help

```
$ napster-to-spotify-sync -h
Usage:
  napster-to-spotify-sync [OPTIONS]

Application Options:
      --spotify-api-client-id=  Spotify API client-ID
      --spotify-api-secret-key= Spotify API secret key
      --napster-api-key=        Napster API key
      --napster-secret-key=     Napster secret key
      --napster-username=       Napster username
      --napster-password=       Napster password
  -p, --playlist-name=          Spotify playlist name
  -a, --only-artists=           One artist to import
  -n, --no-changes              Do not make changes to Spotify
  -m, --spotify-album-market=   Name of music market (two-letter country code) to filter Spotify albums by

Help Options:
  -h, --help                    Show this help message
```
