package gnsssync

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/context"

	"github.com/dsoprea/go-logging"
	"github.com/zmb3/spotify"
)

// Config
const (
	SpotifyAlbumReadBatchSize = 50
)

// Errors
var (
	ErrSpotifyArtistNotFound = fmt.Errorf("artist not found in Spotify")
	ErrSpotifyAlbumNotFound  = fmt.Errorf("album not found in Spotify")
	ErrSpotifyTrackNotFound  = fmt.Errorf("track not found in Spotify")
)

// Cache
var (
	cachedArtists = make(map[string]spotify.ID)
	cachedAlbums  = make(map[albumKey]spotify.ID)
	cachedTracks  = make(map[spotify.ID]map[string]spotify.ID)
)

// Misc
var (
	sLog                = log.NewLogger("gnss.spotify")
	invalidTrackCharsRx *regexp.Regexp
	spaceCharsRx        *regexp.Regexp
	allowCache          = true
)

type albumKey struct {
	artistId  spotify.ID
	albumName string
}

type SpotifyCache struct {
	ctx         context.Context
	spotifyAuth *SpotifyContext

	playlistCache map[string]spotify.ID
	userId        string
}

func NewSpotifyCache(ctx context.Context, spotifyAuth *SpotifyContext) *SpotifyCache {
	playlistCache := make(map[string]spotify.ID)

	return &SpotifyCache{
		ctx:           ctx,
		spotifyAuth:   spotifyAuth,
		playlistCache: playlistCache,
	}
}

func (sc *SpotifyCache) GetSpotifyPlaylistId(spotifyUserId string, playlistName string) (id spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if id, found := sc.playlistCache[playlistName]; found == true {
		return id, nil
	}

	sLog.Debugf(sc.ctx, "Getting playlist ID: [%s]", playlistName)

	splp, err := sc.spotifyAuth.Client.GetPlaylistsForUser(spotifyUserId)
	log.PanicIf(err)

	playlistName = strings.ToLower(playlistName)
	for _, p := range splp.Playlists {
		currentPlaylistName := strings.ToLower(p.Name)

		if currentPlaylistName == playlistName {
			sc.playlistCache[playlistName] = p.ID

			return p.ID, nil
		}
	}

	log.Panic(fmt.Errorf("playlist not found: [%s]", playlistName))

	// Obligatory.
	return spotify.ID(""), nil
}

func (sc *SpotifyCache) GetSpotifyCurrentUserId() (id string, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if sc.userId != "" {
		return sc.userId, nil
	}

	sLog.Debugf(sc.ctx, "Getting current user ID.")

	pu, err := sc.spotifyAuth.Client.CurrentUser()
	log.PanicIf(err)

	sc.userId = pu.ID

	return pu.ID, nil
}

type SpotifyAdapter struct {
	ctx         context.Context
	spotifyAuth *SpotifyContext
}

func NewSpotifyAdapter(ctx context.Context, spotifyAuth *SpotifyContext) *SpotifyAdapter {
	return &SpotifyAdapter{
		ctx:         ctx,
		spotifyAuth: spotifyAuth,
	}
}

func (sa *SpotifyAdapter) getSpotifyArtistId(name string) (id spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	if allowCache {
		if id, found := cachedArtists[name]; found == true {
			return id, nil
		}
	}

	var sr *spotify.SearchResult
	var lastFound spotify.ID

	// Though we support more than one page, we'll limit it to one page for now
	// under the assumption that we should never need to hit the second.
	maxPages := 1

	for j := 0; j < maxPages; j++ {
		sLog.Debugf(nil, "Search for artist [%s] page (%d).", name, j)

		if sr == nil {
			// Extra security due to some concerns that we have.
			if j > 0 {
				log.Panicf("for some reason we aren't search against a new artist page on the second iteration")
			}

			sLog.Debugf(sa.ctx, "Searching for artist: [%s]", name)
			sr, err = sa.spotifyAuth.Client.Search(name, spotify.SearchTypeArtist)
			log.PanicIf(err)
		} else if err := sa.spotifyAuth.Client.NextArtistResults(sr); log.Is(err, spotify.ErrNoMorePages) == true {
			break
		} else if err != nil {
			sLog.Debugf(sa.ctx, "(Retrieving next page of results.)")
			log.Panic(err)
		}

		// Safety.
		if len(sr.Artists.Artists) == 0 {
			log.Panicf("no artists")
		}

		for i, a := range sr.Artists.Artists {
			an := strings.ToLower(a.Name)

			if an == name {
				sLog.Debugf(sa.ctx, "Found ID for artist [%s]: (%d) [%s]", name, i, a.ID)

				if allowCache {
					cachedArtists[name] = a.ID
				}

				lastFound = a.ID
			}
		}

		if lastFound != spotify.ID("") {
			return lastFound, nil
		}
	}

	log.Panic(ErrSpotifyArtistNotFound)
	return spotify.ID(""), nil
}

// removeSuffixClause removes something like "(xyz)" at the very right side of
// the given string.
func (sa *SpotifyAdapter) removeSuffixClause(arg, leftDelimiter, rightDelimiter string) (distilled string) {
	distilled = strings.TrimSpace(arg)
	if distilled[:len(distilled)-1] != rightDelimiter {
		return
	}

	i := strings.LastIndex(distilled, leftDelimiter)

	if i == -1 {
		return distilled
	}

	sLog.Debugf(nil, "Stripping expressions: [%s]", distilled)

	i--

	for i > 0 && string(distilled[i]) == " " {
		i--
	}

	distilled = distilled[:i+1]
	return distilled
}

func (sa *SpotifyAdapter) normalizeTitle(arg string) (distilled string) {
	distilled = arg

	distilled = invalidTrackCharsRx.ReplaceAllString(distilled, "")
	distilled = spaceCharsRx.ReplaceAllString(distilled, " ")
	distilled = strings.ToLower(distilled)

	return distilled
}

func (sa *SpotifyAdapter) simplifyTitle(arg string) (distilled string) {
	distilled = arg

	distilled = sa.removeSuffixClause(distilled, "(", ")")
	distilled = sa.removeSuffixClause(distilled, "[", "]")

	return distilled
}

func (sa *SpotifyAdapter) isEqual(typeName, arg1, arg2 string, doLiberalSearch bool) (isEqual bool, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	// Preprocess the strings.

	arg1 = strings.TrimSpace(arg1)
	arg1 = strings.ToLower(arg1)

	arg2 = strings.TrimSpace(arg2)
	arg2 = strings.ToLower(arg2)

	if doLiberalSearch {
		// Remove subexpressions that may indicate that this album is a
		// variation or alternate production rather than the original.

		arg1 = sa.simplifyTitle(arg1)
		arg2 = sa.simplifyTitle(arg2)

		if arg1 == arg2 {
			return true, nil
		}
	} else {
		// Do a direct string-comparison.

		if arg1 == arg2 {
			return true, nil
		}

		// Remove symbols and extra spacing (some systems might use parantheses
		// and others might use square brackets; they will be equal after
		// this).

		arg1 = sa.normalizeTitle(arg1)
		arg2 = sa.normalizeTitle(arg2)

		if arg1 == arg2 {
			return true, nil
		}
	}

	return false, nil
}

// getSpotifyAlbumId returns a matching Spotify album ID. `doLiberalSearch` can
// be used to find the first match after modifying the list of fetched albums
// to exclude paranthetical expressions at the end of the album names (e.g.
// " (Remastered)") which are sometimes returned instead of the original album
// name that we'd expect to find. In this case, maybe some newer remastered
// album has taken place of the original album in Spotify and the origin album
// in its original quality and with its original name is no longer available.
func (sa *SpotifyAdapter) getSpotifyAlbumId(artistId spotify.ID, name string, marketName string, doLiberalSearch, doPrintCandidates bool) (id spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	sLog.Debugf(nil, "Searching for album [%s] under artist with ID [%s].", name, artistId)

	albumAllowCache := allowCache
	if doLiberalSearch {
		albumAllowCache = false
	}

	cak := albumKey{
		artistId:  artistId,
		albumName: name,
	}

	if albumAllowCache {
		if id, found := cachedAlbums[cak]; found == true {
			return id, nil
		}
	}

	offset := 0
	limit := SpotifyAlbumReadBatchSize

	// Filter by market (otherwise we'll see a lot of duplicates, some of which
	// won't be relevant).
	o := &spotify.Options{
		Offset: &offset,
		Limit:  &limit,
	}

	if marketName != "" {
		o.Country = &marketName
	}

	distilledAvailable := make([]string, 0)

	for {
		ata := spotify.AlbumTypeAlbum
		sp, err := sa.spotifyAuth.Client.GetArtistAlbumsOpt(artistId, o, &ata)
		log.PanicIf(err)

		if len(sp.Albums) == 0 {
			break
		}

		for _, a := range sp.Albums {
			searchableName := strings.ToLower(a.Name)

			albumDescription := fmt.Sprintf("%s (%s)", a.Name, a.AlbumType)
			distilledAvailable = append(distilledAvailable, albumDescription)

			matched, err := sa.isEqual("album", searchableName, name, doLiberalSearch)
			log.PanicIf(err)

			if matched == true {
				sLog.Debugf(sa.ctx, "Found ID for album under artist-ID [%s]: [%s] found as [%s]", artistId, name, searchableName)

				if albumAllowCache {
					cachedAlbums[cak] = a.ID
				}

				return a.ID, nil
			}
		}

		offset := *o.Offset + SpotifyAlbumReadBatchSize
		o.Offset = &offset
	}

	sLog.Debugf(sa.ctx, "Album [%s] under artist-ID [%s] not found (DO-LIBERAL-SEARCH=[%v]).", name, artistId, doLiberalSearch)

	if doPrintCandidates {
		for i, thisName := range distilledAvailable {
			sLog.Debugf(sa.ctx, "Available album under artist-ID [%s]: (%d) [%s]", artistId, i, thisName)
		}
	}

	log.Panic(ErrSpotifyAlbumNotFound)
	return spotify.ID(""), nil
}

// getSpotifyTrackId Find Spotify IDs for the tracks in the given album having
// the given names (after normalizing the names).
func (sa *SpotifyAdapter) getSpotifyTrackIds(albumId spotify.ID, names []string, doPrintCandidates bool) (ids []spotify.ID, missing []string, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	found := false
	var tracks map[string]spotify.ID

	if allowCache {
		tracks, found = cachedTracks[albumId]
	}

	if found == false {
		stp, err := sa.spotifyAuth.Client.GetAlbumTracks(albumId)
		log.PanicIf(err)

		tracks = make(map[string]spotify.ID)
		for _, track := range stp.Tracks {
			spotifyTrackName := sa.normalizeTitle(track.Name)
			tracks[spotifyTrackName] = track.ID
		}

		if allowCache {
			cachedTracks[albumId] = tracks
		}
	}

	ids = make([]spotify.ID, 0)
	missing = make([]string, 0)

	for _, name := range names {
		name = sa.normalizeTitle(name)

		if id, found := tracks[name]; found == true {
			ids = append(ids, id)
			sLog.Debugf(sa.ctx, "Found: [%s] [%s] => [%s]", albumId, name, id)
		} else {
			missing = append(missing, name)
			sLog.Debugf(sa.ctx, "Track [%s] under album-ID [%s] not found.", name, albumId)
		}
	}

	if len(missing) > 0 && doPrintCandidates {
		sLog.Debugf(sa.ctx, "(%d) tracks are available in album-ID [%s].", len(tracks), albumId)

		i := 0
		for thisName, _ := range tracks {
			sLog.Debugf(sa.ctx, "Available track under album-ID [%s]: (%d) [%s]", albumId, i, thisName)

			i++
		}
	}

	return ids, missing, nil
}

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (sa *SpotifyAdapter) getSpotifyTrackId(albumId spotify.ID, name string, doPrintCandidates bool) (id spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	name = sa.normalizeTitle(name)

	found := false
	var tracks map[string]spotify.ID

	if allowCache {
		tracks, found = cachedTracks[albumId]
	}

	if found == false {
		stp, err := sa.spotifyAuth.Client.GetAlbumTracks(albumId)
		log.PanicIf(err)

		tracks = make(map[string]spotify.ID)
		for _, track := range stp.Tracks {
			spotifyTrackName := sa.normalizeTitle(track.Name)
			tracks[spotifyTrackName] = track.ID
		}

		if allowCache {
			cachedTracks[albumId] = tracks
		}
	}

	for albumTrackName, id := range tracks {
		if albumTrackName == name {
			return id, nil
		}
	}

	sLog.Debugf(sa.ctx, "Track [%s] under album-ID [%s] not found.", name, albumId)

	if doPrintCandidates {
		sLog.Debugf(sa.ctx, "(%d) tracks are available in album-ID [%s].", len(tracks), albumId)

		i := 0
		for thisName, _ := range tracks {
			sLog.Debugf(sa.ctx, "Available track under album-ID [%s]: (%d) [%s]", albumId, i, thisName)

			i++
		}
	}

	log.Panic(ErrSpotifyTrackNotFound)
	return spotify.ID(""), nil
}

func (sa *SpotifyAdapter) GetSpotifyTrackIdsWithNames(artistName string, albumName string, tracks []string, marketName string) (foundTracks []spotify.ID, missingTracks []string, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	artistId, err := sa.getSpotifyArtistId(artistName)
	log.PanicIf(err)

	// Do a strict string search to find the album among the candidates.
	albumId, err := sa.getSpotifyAlbumId(artistId, albumName, marketName, false, false)
	if log.Is(err, ErrSpotifyAlbumNotFound) == true {
		// Do a fuzzier search to find the album among the candidates.
		albumId, err = sa.getSpotifyAlbumId(artistId, albumName, marketName, true, true)
		log.PanicIf(err)
	} else if err != nil {
		log.Panic(err)
	}

	foundTracks, missingTracks, err = sa.getSpotifyTrackIds(albumId, tracks, true)
	log.PanicIf(err)

	return foundTracks, missingTracks, nil
}

func (sa *SpotifyAdapter) GetSpotifyTrackIdWithNames(artistName string, albumName string, trackName string, marketName string) (spotifyTrackId spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	artistId, err := sa.getSpotifyArtistId(artistName)
	log.PanicIf(err)

	albumId, err := sa.getSpotifyAlbumId(artistId, albumName, marketName, false, false)
	if log.Is(err, ErrSpotifyAlbumNotFound) == true {
		albumId, err = sa.getSpotifyAlbumId(artistId, albumName, marketName, true, true)
	} else if err != nil {
		log.Panic(err)
	}

	trackId, err := sa.getSpotifyTrackId(albumId, trackName, true)
	log.PanicIf(err)

	return trackId, nil
}

func (sa *SpotifyAdapter) ReadSpotifyPlaylist(playlistId spotify.ID, userId string) (tracks []spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	sLog.Debugf(sa.ctx, "Reading Spotify playlist.")

	ptp, err := sa.spotifyAuth.Client.GetPlaylistTracks(userId, playlistId)
	log.PanicIf(err)

	tracks = make([]spotify.ID, len(ptp.Tracks))
	for j, pt := range ptp.Tracks {
		tracks[j] = pt.Track.ID
	}

	return tracks, nil
}

func init() {
	var err error

	invalidTrackCharsRx, err = regexp.Compile("[^a-zA-Z0-9']+")
	log.PanicIf(err)

	spaceCharsRx, err = regexp.Compile("[ ]+")
	log.PanicIf(err)
}
