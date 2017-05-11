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
    ErrTrackNotFoundInSpotify = fmt.Errorf("track not found in Spotify")
    ErrAlbumNotFoundInSpotify = fmt.Errorf("album not found in Spotify")
    ErrArtistNotFoundInSpotify = fmt.Errorf("artist not found in Spotify")
)

// Cache
var (
    cachedArtists = make(map[string]spotify.ID)
    cachedAlbums = make(map[cachedAlbumKey]spotify.ID)
    cachedTracks = make(map[spotify.ID]map[string]spotify.ID)
)

// Misc
var (
    sLog = log.NewLogger("gnss.spotify")
    invalidTrackChars *regexp.Regexp
    allowCache = true
)


type cachedAlbumKey struct {
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

    name = strings.ToLower(name)

    if allowCache {
        if id, found := cachedArtists[name]; found == true {
            return id, nil
        }
    }

    var sr *spotify.SearchResult

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

        for _, a := range sr.Artists.Artists {
            an := strings.ToLower(a.Name)

            if an == name {
                if allowCache {
                    cachedArtists[name] = a.ID
                }

                return a.ID, nil
            }
        }
    }

    log.Panic(ErrArtistNotFoundInSpotify)
    return spotify.ID(""), nil
}

func (sa *SpotifyAdapter) getSpotifyAlbumId(artistId spotify.ID, name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    name = strings.ToLower(name)

    cak := cachedAlbumKey{
        artistId: artistId,
        albumName: name,
    }

    if allowCache {
        if id, found := cachedAlbums[cak]; found == true {
            return id, nil
        }
    }

    sp, err := spotify.GetArtistAlbums(artistId)
    log.PanicIf(err)

    for _, a := range sp.Albums {
        if a.Name == name {
            if allowCache {
                cachedAlbums[cak] = a.ID
            }

            return a.ID, nil
        }
    }

    log.Panic(ErrAlbumNotFoundInSpotify)
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
        stp, err := spotify.GetAlbumTracks(id)
        log.PanicIf(err)

        tracks = make(map[string]spotify.ID)
        for _, track := range stp.Tracks {
            spotifyTrackName := invalidTrackChars.ReplaceAllString(track.Name, "")
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

    log.Panic(ErrTrackNotFoundInSpotify)
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
