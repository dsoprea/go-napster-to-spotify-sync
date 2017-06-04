package gnsssync

import (
	"fmt"
	"sort"
	"strings"

	"net/http"

	"golang.org/x/net/context"

	"github.com/dsoprea/go-logging"
	"github.com/dsoprea/go-napster"
	"github.com/zmb3/spotify"
)

// Misc
var (
	iLog = log.NewLogger("gnss.import")
)

type albumKeyNames struct {
	artistName string
	albumName  string
}

type TrackInfo struct {
	ArtistName string
	AlbumName  string
	TitleName  string
}

func (ti TrackInfo) String() string {
	return fmt.Sprintf("TRACK<[%s] [%s] [%s]>", ti.ArtistName, ti.AlbumName, ti.TitleName)
}

type Importer struct {
	ctx context.Context
	hc  *http.Client

	napsterApiKey    string
	napsterSecretKey string
	napsterUsername  string
	napsterPassword  string

	spotifyAuth *SpotifyContext
	sc          *SpotifyCache
	sa          *SpotifyAdapter

	batchSize int

	spotifyIndex  map[spotify.ID]bool
	artistNotices map[string]bool

	marketName string
}

// NewImporter creates an Importer instance. `marketName` can be the name of a
// market to filter albums by or empty.
func NewImporter(ctx context.Context, napsterApiKey, napsterSecretKey, napsterUsername, napsterPassword string, spotifyAuth *SpotifyContext, spotifyCache *SpotifyCache, batchSize int, marketName string) *Importer {
	hc := new(http.Client)

	spotifyIndex := make(map[spotify.ID]bool)
	artistNotices := make(map[string]bool)

	sa := NewSpotifyAdapter(ctx, spotifyAuth)

	return &Importer{
		ctx: ctx,
		hc:  hc,

		napsterApiKey:    napsterApiKey,
		napsterSecretKey: napsterSecretKey,
		napsterUsername:  napsterUsername,
		napsterPassword:  napsterPassword,

		spotifyAuth: spotifyAuth,
		sc:          spotifyCache,
		sa:          sa,

		batchSize: batchSize,

		spotifyIndex:  spotifyIndex,
		artistNotices: artistNotices,

		marketName: marketName,
	}
}

type NormalizedTrack struct {
	ArtistName string
	AlbumName  string
	TrackName  string
}

func (nt NormalizedTrack) String() string {
	return fmt.Sprintf("TRACK<[%s] [%s] [%s]>", nt.ArtistName, nt.AlbumName, nt.TrackName)
}

func (i *Importer) getNapsterNormalizedTrack(track *napster.MetadataTrackDetail) *NormalizedTrack {
	artistName := strings.ToLower(track.ArtistName)
	trackName := strings.ToLower(track.Name)
	albumName := strings.ToLower(track.AlbumName)

	return &NormalizedTrack{
		TrackName:  trackName,
		AlbumName:  albumName,
		ArtistName: artistName,
	}
}

func (i *Importer) readNapsterFavorites(amc *napster.AuthenticatedMemberClient, onlyArtists []string) (groupedTracks map[albumKeyNames][]string, skipped int, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	mc := napster.NewMetadataClient(i.ctx, i.hc, i.napsterApiKey)

	groupedTracks = make(map[albumKeyNames][]string)
	j := 0
	for {
		favorites, err := amc.GetFavoriteTracks(j, i.batchSize)
		log.PanicIf(err)

		favoritesLen := len(favorites)
		if favoritesLen == 0 {
			break
		}

		iLog.Debugf(i.ctx, "(%d) favorite tracks received starting at index (%d).", favoritesLen, j)

		j += favoritesLen

		ids := make([]string, favoritesLen)
		for i, info := range favorites {
			ids[i] = info.Id
		}

		tracks, err := mc.GetTrackDetail(ids...)
		log.PanicIf(err)

		for _, track := range tracks {
			// We're going to check a couple of different things and be
			// discriminating in what we print. This should allow us to
			// efficiently cherry-pick artists, maybe even one at a time, to
			// add to the playlist.

			nt := i.getNapsterNormalizedTrack(&track)

			// One of the artists on the track must be in the `onlyArtists`
			// list. If track is *not* in Spotify and not in the `onlyArtists`
			// list, skip and print.
			//
			// Our complexity is higher because each track is associated with
			// potentially more than one artist.

			found := false
			for _, anAllowed := range onlyArtists {
				if anAllowed == nt.ArtistName {
					found = true
					break
				}
			}

			if found == false {
				skipped++

				i.artistNotices[nt.ArtistName] = true

				continue
			}

			// Added.

			akn := albumKeyNames{
				artistName: nt.ArtistName,
				albumName:  nt.AlbumName,
			}

			if groupedTracksList, found := groupedTracks[akn]; found == true {
				groupedTracks[akn] = append(groupedTracksList, nt.TrackName)
			} else {
				groupedTracks[akn] = []string{nt.TrackName}
			}
		}
	}

	return groupedTracks, skipped, nil
}

func (i *Importer) importFavorites(amc *napster.AuthenticatedMemberClient, onlyArtists []string, collector *trackCollector, missing []string) (count int, skipped int, missingUpdated []string, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if len(onlyArtists) == 0 {
		log.Panic(fmt.Errorf("at least one artist must be given to import"))
	}

	groupedTracks, skipped, err := i.readNapsterFavorites(amc, onlyArtists)
	log.PanicIf(err)

	if len(groupedTracks) == 0 {
		return 0, 0, nil, nil
	}

	missingArtists := make(map[string]bool)
	missingAlbums := make(map[albumKeyNames]bool)

	added := 0
	for akn, tracks := range groupedTracks {
		// If track is not in Spotify and *in* the list, print and add.
		//
		// Note that this struct will only have exactly one artist (Napster only returns one).

		artistPhrase := fmt.Sprintf("[%s]", akn.artistName)
		albumPhrase := fmt.Sprintf("[%s] [%s]", akn.artistName, akn.albumName)

		// Short circuit if we've previously missed on this artist or album.

		if _, found := missingArtists[akn.artistName]; found == true {
			continue
		}

		if _, found := missingAlbums[akn]; found == true {
			continue
		}

		// Do the lookup.

		spotifyTrackIds, missingTrackNames, err := i.sa.GetSpotifyTrackIdsWithNames(akn.artistName, akn.albumName, tracks, i.marketName)
		if log.Is(err, ErrSpotifyArtistNotFound) == true {
			if _, found := missingArtists[akn.artistName]; found == false {
				missing = append(missing, artistPhrase)
				missingArtists[akn.artistName] = true

				iLog.Warningf(i.ctx, "ARTIST NOT FOUND IN SPOTIFY: %s", artistPhrase)
			}

			continue
		} else if log.Is(err, ErrSpotifyAlbumNotFound) == true {
			if _, found := missingAlbums[akn]; found == false {
				missing = append(missing, albumPhrase)
				missingAlbums[akn] = true

				iLog.Warningf(i.ctx, "ALBUM NOT FOUND IN SPOTIFY: %s", albumPhrase)
			}

			continue
		} else if err != nil {
			log.Panic(err)
		}

		if len(missingTrackNames) > 0 {
			for _, trackName := range missingTrackNames {
				trackPhrase := fmt.Sprintf("[%s] [%s] [%s]", akn.artistName, akn.albumName, trackName)

				missing = append(missing, trackPhrase)
				iLog.Warningf(i.ctx, "TRACK NOT FOUND IN SPOTIFY: %s", trackPhrase)
			}
		}

		if len(spotifyTrackIds) == 0 {
			iLog.Warningf(i.ctx, "No favorite tracks from this album were found.")
			continue
		}

		// If track is already in Spotify, don't do or print anything.

		for spotifyTrackId, name := range spotifyTrackIds {
			if _, found := i.spotifyIndex[spotifyTrackId]; found == true {
				iLog.Infof(nil, "Track already in playlist: [%s]", spotifyTrackId)
				continue
			}

			iLog.Infof(i.ctx, "WILL ADD: [%s] [%s] [%s] -> [%s]", akn.artistName, akn.albumName, name, spotifyTrackId)
			collector.ids[spotifyTrackId] = TrackInfo{
				ArtistName: akn.artistName,
				AlbumName:  akn.albumName,
				TitleName:  name,
			}

			added++
		}
	}

	iLog.Debugf(i.ctx, "STATS: ADDED=(%d) SKIPPED=(%d) MISSING=(%d)", added, skipped, len(missing))

	return added, skipped, missing, nil
}

func (i *Importer) buildSpotifyIndex(tracks []spotify.ID) (err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	iLog.Debugf(i.ctx, "Building index with (%d) existing songs.", len(tracks))

	for _, id := range tracks {
		i.spotifyIndex[id] = true
	}

	return nil
}

func (i *Importer) preloadExisting(spotifyPlaylistName, spotifyMarketName string) (err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	spotifyUserId, err := i.sc.GetSpotifyCurrentUserId()
	log.PanicIf(err)

	spotifyPlaylistId, err := i.sc.GetSpotifyPlaylistId(spotifyUserId, spotifyPlaylistName)
	log.PanicIf(err)

	spotifyTracks, err := i.sa.ReadSpotifyPlaylist(spotifyPlaylistId, spotifyUserId, spotifyMarketName)
	log.PanicIf(err)

	err = i.buildSpotifyIndex(spotifyTracks)
	log.PanicIf(err)

	return nil
}

// trackCollector Keeps track of the tracks that need to be added. We're going
// to minimize our requests.
type trackCollector struct {
	ids map[spotify.ID]TrackInfo
}

func (i *Importer) GetTracksToAdd(spotifyPlaylistName string, onlyArtists []string, spotifyMarketName string) (tracks map[spotify.ID]TrackInfo, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	// Make artists lower-case.
	for i, a := range onlyArtists {
		onlyArtists[i] = strings.ToLower(a)
	}

	if err := i.preloadExisting(spotifyPlaylistName, spotifyMarketName); err != nil {
		log.Panic(err)
	}

	iLog.Infof(i.ctx, "Reading Napster favorites.")

	a := napster.NewAuthenticator(i.ctx, i.hc, i.napsterApiKey, i.napsterSecretKey)
	a.SetUserCredentials(i.napsterUsername, i.napsterPassword)

	collector := new(trackCollector)
	collector.ids = make(map[spotify.ID]TrackInfo)

	amc := napster.NewAuthenticatedMemberClient(i.ctx, i.hc, a)

	missing := make([]string, 0)

	_, skipped, missing, err := i.importFavorites(amc, onlyArtists, collector, missing)
	log.PanicIf(err)

	if len(i.artistNotices) > 0 {
		ignoredArtists := make([]string, len(i.artistNotices))

		j := 0
		for an, _ := range i.artistNotices {
			ignoredArtists[j] = an
			j++
		}

		ans := sort.StringSlice(ignoredArtists)
		ans.Sort()

		for _, an := range ans {
			iLog.Warningf(i.ctx, "IGNORING ARTIST: [%s]", an)
		}
	}

	len_ := len(collector.ids)

	iLog.Infof(i.ctx, "(%d) tracks found to import.", len_)
	iLog.Infof(i.ctx, "(%d) tracks skipped.", skipped)

	for j, missingPhrase := range missing {
		iLog.Infof(i.ctx, "NOT FOUND: (%d) %s", j, missingPhrase)
	}

	return collector.ids, nil
}
