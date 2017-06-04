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
	SpotifyReadBatchSize = 50
)

// Errors
var (
	ErrSpotifyArtistNotFound = fmt.Errorf("artist not found in Spotify")
	ErrSpotifyAlbumNotFound  = fmt.Errorf("album not found in Spotify")
	ErrSpotifyTrackNotFound  = fmt.Errorf("track not found in Spotify")
)

// Cache
var (
	cachedArtists = make(map[string][]spotify.ID)
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

func (sa *SpotifyAdapter) searchSpotifyArtists(name string) (ids []spotify.ID, err error) {
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

	sLog.Debugf(nil, "Search for artist [%s].", name)

	var sr *spotify.SearchResult

	// Though we support more than one page, we'll limit it to one page for now
	// under the assumption that we should never need to hit the second.
	maxPages := 1

	matching := make([]spotify.ID, 0)
	for j := 0; j < maxPages; j++ {
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

		for _, a := range sr.Artists.Artists {
			an := strings.ToLower(a.Name)

			if an == name {
				matching = append(matching, a.ID)
			}
		}
	}

	if len(matching) > 0 {
		if allowCache {
			cachedArtists[name] = matching
		}

		return matching, nil
	}

	log.Panic(ErrSpotifyArtistNotFound)
	return []spotify.ID{}, nil
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

	distilled = invalidTrackCharsRx.ReplaceAllString(distilled, " ")
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

	sLog.Debugf(nil, "Searching for album [%s] under artist with ID [%s].", name, artistId)

	offset := 0
	limit := SpotifyReadBatchSize

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

		len_ := len(sp.Albums)
		if len_ == 0 {
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

		offset := *o.Offset + len_
		o.Offset = &offset
	}

	sLog.Debugf(sa.ctx, "Album [%s] under artist-ID [%s] not found (DO-LIBERAL-SEARCH=[%v]).", name, artistId, doLiberalSearch)

	if doPrintCandidates {
		sLog.Debugf(sa.ctx, "(%d) other albums were found under artist-ID [%s].", len(distilledAvailable), artistId)
		for i, thisName := range distilledAvailable {
			sLog.Debugf(sa.ctx, "Available album under artist-ID [%s]: (%d) [%s]", artistId, i, thisName)
		}
	}

	log.Panic(ErrSpotifyAlbumNotFound)
	return spotify.ID(""), nil
}

// getSpotifyTrackId Find Spotify IDs for the tracks in the given album having
// the given names (after normalizing the names).
func (sa *SpotifyAdapter) getSpotifyTrackIds(albumId spotify.ID, names []string, doPrintCandidates bool) (ids map[spotify.ID]string, missing []string, err error) {
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
		i := 0
		tracks = make(map[string]spotify.ID)
		for {
			stp, err := sa.spotifyAuth.Client.GetAlbumTracksOpt(albumId, SpotifyReadBatchSize, i)
			log.PanicIf(err)

			if len(stp.Tracks) == 0 {
				break
			}

			for _, track := range stp.Tracks {
				spotifyTrackName := sa.normalizeTitle(track.Name)
				tracks[spotifyTrackName] = track.ID

				i++
			}
		}

		if allowCache {
			cachedTracks[albumId] = tracks
		}
	}

	ids = make(map[spotify.ID]string)
	missing = make([]string, 0)

	for _, name := range names {
		name = sa.normalizeTitle(name)

		if id, found := tracks[name]; found == true {
			ids[id] = name
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

func (sa *SpotifyAdapter) GetSpotifyTrackIdsWithNames(artistName string, albumName string, tracks []string, marketName string) (foundTracks map[spotify.ID]string, missingTracks []string, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	artistIds, err := sa.searchSpotifyArtists(artistName)
	log.PanicIf(err)

	// TODO(dustin): !! IMPORTANT: We should search all matching albums (not just stopping when we find a match) and use theone that has the least number of missing albums. Otherwise, we can hit on special albums but miss the origin albums.

	// Search for the albums name using a strict string search.

	for _, artistId := range artistIds {
		// Do a strict string search to find the album among the candidates.
		albumId, err := sa.getSpotifyAlbumId(artistId, albumName, marketName, false, false)
		if err != nil {
			if log.Is(err, ErrSpotifyAlbumNotFound) == true {
				continue
			} else {
				log.Panic(err)
			}
		}

		foundTracks, missingTracks, err = sa.getSpotifyTrackIds(albumId, tracks, true)
		log.PanicIf(err)

		if len(foundTracks) == 0 {
			continue
		}

		return foundTracks, missingTracks, nil
	}

	// We could find either the album or any of our tracks under this artist.
	// Try with a more fuzzy album search.

	for _, artistId := range artistIds {
		// Do a fuzzy string search to find the album among the candidates.
		albumId, err := sa.getSpotifyAlbumId(artistId, albumName, marketName, true, true)
		if err != nil {
			if log.Is(err, ErrSpotifyAlbumNotFound) == true {
				continue
			} else {
				log.Panic(err)
			}
		}

		foundTracks, missingTracks, err = sa.getSpotifyTrackIds(albumId, tracks, true)
		log.PanicIf(err)

		if len(foundTracks) == 0 {
			continue
		}

		return foundTracks, missingTracks, nil
	}

	// If we got as far as searching for tracks, obviously we found at least
	// one matching artist with one matching album. If we searched for tracks
	// the `foundTracks` (and `missingTracks`) will be empty rather than `nil`.
	if foundTracks != nil {
		return foundTracks, missingTracks, nil
	}

	// No matching albums were found in any of the matching artists.
	log.Panic(ErrSpotifyAlbumNotFound)
	return nil, nil, nil
}

/*
func (sa *SpotifyAdapter) GetSpotifyTrackIdWithNames(artistName string, albumName string, trackName string, marketName string) (spotifyTrackId spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	artistId, err := sa.searchSpotifyArtists(artistName)
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
*/

func (sa *SpotifyAdapter) ReadSpotifyPlaylist(playlistId spotify.ID, userId string, marketName string) (tracks []spotify.ID, err error) {
	defer func() {
		if state := recover(); state != nil {
			err = log.Wrap(state.(error))
		}
	}()

	sLog.Debugf(sa.ctx, "Reading Spotify playlist.")

	// TODO(dustin): !! Finish refactoring to read all songs by batches.

	offset := 0
	limit := SpotifyReadBatchSize

	// Filter by market (otherwise we'll see a lot of duplicates, some of which
	// won't be relevant).
	o := &spotify.Options{
		Offset: &offset,
		Limit:  &limit,
	}

	if marketName != "" {
		o.Country = &marketName
	}

	tracks = make([]spotify.ID, 0)

	for {
		ptp, err := sa.spotifyAuth.Client.GetPlaylistTracksOpt(userId, playlistId, o, "")
		log.PanicIf(err)

		if len(ptp.Tracks) == 0 {
			break
		}

		for _, pt := range ptp.Tracks {
			tracks = append(tracks, pt.Track.ID)
		}

		offset := *o.Offset + len(ptp.Tracks)
		o.Offset = &offset
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
