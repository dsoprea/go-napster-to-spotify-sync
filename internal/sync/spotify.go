package gnsssync

import (
    "fmt"
    "strings"
    "regexp"

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
    ErrSpotifyAlbumNotFound = fmt.Errorf("album not found in Spotify")
    ErrSpotifyTrackNotFound = fmt.Errorf("track not found in Spotify")
)

// Cache
var (
    cachedArtists = make(map[string]spotify.ID)
    cachedAlbums = make(map[albumKey]spotify.ID)
    cachedTracks = make(map[spotify.ID]map[string]spotify.ID)
)

// Misc
var (
    sLog = log.NewLogger("gnss.spotify")
    invalidTrackCharsRx *regexp.Regexp
    spaceCharsRx *regexp.Regexp
    allowCache = true
)


type albumKey struct {
    artistId spotify.ID
    albumName string
}


type SpotifyCache struct {
    ctx context.Context
    spotifyAuth *SpotifyContext

    playlistCache map[string]spotify.ID
    userId string
}

func NewSpotifyCache(ctx context.Context, spotifyAuth *SpotifyContext) *SpotifyCache {
    playlistCache := make(map[string]spotify.ID)

    return &SpotifyCache{
        ctx: ctx,
        spotifyAuth: spotifyAuth,
        playlistCache: playlistCache,
    }
}

func (sc *SpotifyCache) GetSpotifyPlaylistId(spotifyUserId string, playlistName string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
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
            err = state.(error)
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
    ctx context.Context
    spotifyAuth *SpotifyContext
}

func NewSpotifyAdapter(ctx context.Context, spotifyAuth *SpotifyContext) *SpotifyAdapter {
    return &SpotifyAdapter{
        ctx: ctx,
        spotifyAuth: spotifyAuth,
    }
}

func (sa *SpotifyAdapter) getSpotifyNormalizedTrack(ft *spotify.FullTrack) *NormalizedTrack {
    artistNames := make([]string, len(ft.Artists))
    for i, a := range ft.Artists {
        currentArtistName := strings.ToLower(a.Name)
        artistNames[i] = currentArtistName
    }

    albumName := strings.ToLower(ft.Album.Name)
    trackName := strings.ToLower(ft.Name)

    return &NormalizedTrack{
        TrackName: trackName,
        AlbumName: albumName,
        ArtistNames: artistNames,
    }
}

func (sa *SpotifyAdapter) getSpotifyArtistId(name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
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
        } else if err := sa.spotifyAuth.Client.NextArtistResults(sr); err == spotify.ErrNoMorePages {
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
// TODO(dustin): !! We're currently scanning the entire list of matching artists just to get a feel for how many matching artists there are. This might explain why we don't find all of the albums under the first match. UPDATE: Doesn't appear to be a problem.
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
            err = state.(error)
        }
    }()

    sLog.Debugf(nil, "Searching for album [%s] under artist with ID [%s].", name, artistId)

    albumAllowCache := allowCache
    if doLiberalSearch {
        albumAllowCache = false
    }

    cak := albumKey{
        artistId: artistId,
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
        Limit: &limit,
    }

    if marketName != "" {
        o.Country = &marketName
    }

    distilledAvailable := make([]string, 0)

    for {
        ata := spotify.AlbumTypeAlbum
        sp, err := spotify.spotifyAuth.Client.GetArtistAlbumsOpt(artistId, o, &ata)
        log.PanicIf(err)

        if len(sp.Albums) == 0 {
            break
        }

        for _, a := range sp.Albums {
            if a.AlbumType != "album" {
                continue
            }

            thisName := strings.ToLower(a.Name)
            distilledAvailable = append(distilledAvailable, a.Name)

            searchableName := thisName

            if doLiberalSearch {
// TODO(dustin): Cache this.
                i := strings.LastIndex(searchableName, "(")

                if i > -1 {
                    sLog.Debugf(nil, "Stripping any paranthetical expressions from album name: [%s]", searchableName)

                    i--

                    for i > 0 && string(searchableName[i]) == " " {
                        i--
                    }

                    searchableName = searchableName[:i + 1]
                }
            }

            if searchableName == name {
                sLog.Debugf(sa.ctx, "Found ID for album under artist-ID [%s]: [%s] found as [%s]", artistId, name, thisName)

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

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (sa *SpotifyAdapter) getSpotifyTrackId(albumId spotify.ID, name string, doPrintCandidates bool) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    name = invalidTrackCharsRx.ReplaceAllString(name, "")
    name = spaceCharsRx.ReplaceAllString(name, " ")

    found := false
    var tracks map[string]spotify.ID

    if allowCache {
        tracks, found = cachedTracks[albumId]
    }

    if found == false {
        stp, err := spotify.spotifyAuth.Client.GetAlbumTracks(albumId)
        log.PanicIf(err)

        tracks = make(map[string]spotify.ID)
        for _, track := range stp.Tracks {
            spotifyTrackName := invalidTrackCharsRx.ReplaceAllString(track.Name, "")
            spotifyTrackName = spaceCharsRx.ReplaceAllString(spotifyTrackName, " ")
            spotifyTrackName = strings.ToLower(spotifyTrackName)

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

func (sa *SpotifyAdapter) GetSpotifyTrackIdWithNames(artistName string, albumName string, trackName string, marketName string) (spotifyTrackId spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    artistId, err := sa.getSpotifyArtistId(artistName)
    log.PanicIf(err)

    albumId, err := sa.getSpotifyAlbumId(artistId, albumName, marketName, false, false)
    if log.Is(err, ErrSpotifyAlbumNotFound) == true {
        albumId, err = sa.getSpotifyAlbumId(artistId, albumName, marketName, true, true)
    }

    log.PanicIf(err)

    trackId, err := sa.getSpotifyTrackId(albumId, trackName, true)
    log.PanicIf(err)

    return trackId, nil
}

func (sa *SpotifyAdapter) ReadSpotifyPlaylist(playlistId spotify.ID, userId string) (tracks []spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
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

    invalidTrackCharsRx, err = regexp.Compile("[^a-zA-Z0-9' ]+")
    log.PanicIf(err)

    spaceCharsRx, err = regexp.Compile("[ ]+")
    log.PanicIf(err)
}
