package gnsssync

import (
    "fmt"
    "strings"
    "regexp"

    "golang.org/x/net/context"

    "github.com/dsoprea/go-logging"
    "github.com/zmb3/spotify"
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
    invalidTrackChars *regexp.Regexp
    allowCache = true
    printCandidates = true
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

    for {
        if sr == nil {
            sLog.Debugf(sa.ctx, "Searching for artist: [%s]", name)
            sr, err = sa.spotifyAuth.Client.Search(name, spotify.SearchTypeArtist)
            log.PanicIf(err)
        } else if err := sa.spotifyAuth.Client.NextArtistResults(sr); err == spotify.ErrNoMorePages {
            break
        } else if err != nil {
            sLog.Debugf(sa.ctx, "(Retrieving next page of results.)")
            log.Panic(err)
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
    }

    if lastFound != spotify.ID("") {
        return lastFound, nil
    }

    log.Panic(ErrSpotifyArtistNotFound)
    return spotify.ID(""), nil
}

func (sa *SpotifyAdapter) getSpotifyAlbumId(artistId spotify.ID, name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    cak := albumKey{
        artistId: artistId,
        albumName: name,
    }

    if allowCache {
        if id, found := cachedAlbums[cak]; found == true {
            return id, nil
        }
    }

// TODO(dustin): !! This isn't returning all albums (e.g. 3 Doors Down's "Away From the Sun").
// TODO(dustin): Reimplement using GetArtistAlbumsOpt() so that we can pass the type.
    sp, err := spotify.GetArtistAlbums(artistId)
    log.PanicIf(err)

    distilledAvailable := make([]string, len(sp.Albums))
    for i, a := range sp.Albums {
        if a.AlbumType != "album" {
            continue
        }

        thisName := strings.ToLower(a.Name)
        distilledAvailable[i] = thisName

        if thisName == name {
            sLog.Debugf(sa.ctx, "Found ID for album [%s] under artist-ID [%s]: [%s]", name, artistId, name)

            if allowCache {
                cachedAlbums[cak] = a.ID
            }

            return a.ID, nil
        }
    }

    sLog.Debugf(sa.ctx, "Album [%s] under artist-ID [%s] not found.", name, artistId)

    if printCandidates {
        for i, thisName := range distilledAvailable {
            sLog.Debugf(sa.ctx, "Available album under artist-ID [%s]: (%d) [%s]", artistId, i, thisName)
        }
    }

    log.Panic(ErrSpotifyAlbumNotFound)
    return spotify.ID(""), nil
}

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (sa *SpotifyAdapter) getSpotifyTrackId(albumId spotify.ID, name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    name = invalidTrackChars.ReplaceAllString(name, "")

    found := false
    var tracks map[string]spotify.ID

    if allowCache {
        tracks, found = cachedTracks[albumId]
    }

    if found == false {
        stp, err := spotify.GetAlbumTracks(albumId)
        log.PanicIf(err)

        tracks = make(map[string]spotify.ID)
        for _, track := range stp.Tracks {
            spotifyTrackName := invalidTrackChars.ReplaceAllString(track.Name, "")
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

    if printCandidates {
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

func (sa *SpotifyAdapter) GetSpotifyTrackIdWithNames(artistName string, albumName string, trackName string) (spotifyTrackId spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    artistId, err := sa.getSpotifyArtistId(artistName)
    log.PanicIf(err)

    albumId, err := sa.getSpotifyAlbumId(artistId, albumName)
    log.PanicIf(err)

    trackId, err := sa.getSpotifyTrackId(albumId, trackName)
    log.PanicIf(err)

    return trackId, nil
}

func (sa *SpotifyAdapter) ReadSpotifyPlaylist(playlistId spotify.ID, userId string) (tracks []*NormalizedTrack, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    sLog.Debugf(sa.ctx, "Reading Spotify playlist.")

    ptp, err := sa.spotifyAuth.Client.GetPlaylistTracks(userId, playlistId)
    log.PanicIf(err)

    tracks = make([]*NormalizedTrack, len(ptp.Tracks))
    for j, pt := range ptp.Tracks {
        nt := sa.getSpotifyNormalizedTrack(&pt.Track)
        tracks[j] = nt
    }

    return tracks, nil
}

func init() {
    var err error
    invalidTrackChars, err = regexp.Compile("[^a-zA-Z0-9' ]+")
    log.PanicIf(err)
}
