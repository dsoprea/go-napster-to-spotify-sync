package gnsssync

import (
    "fmt"
    "strings"
    "sort"
    "regexp"

    "net/http"

    "golang.org/x/net/context"

    "github.com/dsoprea/go-logging"
    "github.com/dsoprea/go-napster"
    "github.com/zmb3/spotify"
    "github.com/randomingenuity/go-ri/common"
)

// Errors
var (
    ErrTrackNotFoundInSpotify = fmt.Errorf("track not found in Spotify")
    ErrAlbumNotFoundInSpotify = fmt.Errorf("album not found in Spotify")
    ErrArtistNotFoundInSpotify = fmt.Errorf("artist not found in Spotify")
)

// Misc
var (
    iLog = log.NewLogger("gnss.import")
    invalidTrackChars *regexp.Regexp
)


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

    iLog.Debugf(sc.ctx, "Getting playlist ID: [%s]", playlistName)

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

    iLog.Debugf(sc.ctx, "Getting current user ID.")

    pu, err := sc.spotifyAuth.Client.CurrentUser()
    log.PanicIf(err)

    sc.userId = pu.ID

    return pu.ID, nil
}


type Importer struct {
    ctx context.Context
    hc *http.Client

    napsterApiKey string
    napsterSecretKey string
    napsterUsername string
    napsterPassword string

    spotifyAuth *SpotifyContext
    sc *SpotifyCache

    batchSize int
    offset int

    spotifyIndex map[string]bool
    artistNotices map[string]bool
}

func NewImporter(ctx context.Context, napsterApiKey, napsterSecretKey, napsterUsername, napsterPassword string, spotifyAuth *SpotifyContext, spotifyCache *SpotifyCache, batchSize int) *Importer {
    hc := new(http.Client)

    spotifyIndex := make(map[string]bool)
    artistNotices := make(map[string]bool)

    return &Importer{
        ctx: ctx,
        hc: hc,

        napsterApiKey: napsterApiKey,
        napsterSecretKey: napsterSecretKey,
        napsterUsername: napsterUsername,
        napsterPassword: napsterPassword,

        spotifyAuth: spotifyAuth,
        sc: spotifyCache,

        batchSize: batchSize,
        offset: 0,

        spotifyIndex: spotifyIndex,
        artistNotices: artistNotices,
    }
}


type NormalizedTrack struct {
    ArtistNames []string
    AlbumName string
    TrackName string
}

func (nt NormalizedTrack) String() string {
    return fmt.Sprintf("TRACK<%v [%s] [%s]>", nt.ArtistNames, nt.AlbumName, nt.TrackName)
}

func (nt *NormalizedTrack) Hash() string {
    parts := append([]string {}, nt.AlbumName, nt.TrackName)
    parts = append(parts, nt.ArtistNames...)

    return ricommon.EncodeStringsToSha1DigestString(parts)
}

func (i *Importer) getSpotifyNormalizedTrack(ft *spotify.FullTrack) *NormalizedTrack {
    artistNames := make([]string, len(ft.Artists))
    for i, sa := range ft.Artists {
        currentArtistName := strings.ToLower(sa.Name)
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

func (i *Importer) getSpotifyArtistId(name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    name = strings.ToLower(name)

// TODO(dustin): !! Add caching.

    var sr *spotify.SearchResult

    for {
        if sr == nil {
            iLog.Debugf(i.ctx, "Searching for artist: [%s]", name)
            sr, err = i.spotifyAuth.Client.Search(name, spotify.SearchTypeArtist)
            log.PanicIf(err)
        } else if err := i.spotifyAuth.Client.NextArtistResults(sr); err == spotify.ErrNoMorePages {
            break
        } else if err != nil {
            iLog.Debugf(i.ctx, "(Retrieving next page of results.)")
            log.Panic(err)
        }

        for _, a := range sr.Artists.Artists {
            an := strings.ToLower(a.Name)

            if an == name {
                return a.ID, nil
            }
        }
    }

    log.Panic(ErrArtistNotFoundInSpotify)
    return spotify.ID(""), nil
}

func (i *Importer) getSpotifyAlbumId(artistId spotify.ID, name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    name = strings.ToLower(name)

// TODO(dustin): !! Add caching.

    sp, err := spotify.GetArtistAlbums(artistId)
    log.PanicIf(err)

    for _, sa := range sp.Albums {
        if sa.Name == name {
            return sa.ID, nil
        }
    }

    log.Panic(ErrAlbumNotFoundInSpotify)
    return spotify.ID(""), nil
}

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (i *Importer) getSpotifyTrackId(albumId spotify.ID, name string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

// TODO(dustin): !! Add caching.

    stp, err := spotify.GetAlbumTracks(id)
    log.PanicIf(err)

    name = invalidTrackChars.ReplaceAllString(name, "")

    for _, track := range stp.Tracks {
        spotifyTrackName := invalidTrackChars.ReplaceAllString(track.Name, "")

        if spotifyTrackName == name {
            return track.ID, nil
        }
    }

    log.Panic(ErrTrackNotFoundInSpotify)
    return spotify.ID(""), nil
}

func (i *Importer) getSpotifyTrackIdWithNames(artistName string, albumName string, trackName string) (spotifyTrackId spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

// TODO(dustin): !! We might just consolidate all of the caching here.

    artistId, err := i.getSpotifyArtistId(artistName)
    log.PanicIf(err)

    albumId, err := i.getSpotifyAlbumId(artistId, albumName)
    log.PanicIf(err)

    trackId, err := i.getSpotifyTrackId(albumId, trackName)
    log.PanicIf(err)

    return trackId, nil
}

func (i *Importer) getNapsterNormalizedTrack(track *napster.MetadataTrackDetail) *NormalizedTrack {
    artistNames := []string { strings.ToLower(track.ArtistName) }

    trackName := strings.ToLower(track.Name)
    albumName := strings.ToLower(track.AlbumName)

    return &NormalizedTrack{
        TrackName: trackName,
        AlbumName: albumName,
        ArtistNames: artistNames,
    }
}

func (i *Importer) importBatch(amc *napster.AuthenticatedMemberClient, onlyArtists []string, collector *trackCollector, missing []string) (count int, skipped int, missingUpdated []string, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    if len(onlyArtists) == 0 {
        log.Panic(fmt.Errorf("at least one artist must be given to import"))
    }

    trackInfo, err := amc.GetFavoriteTracks(i.offset, i.batchSize)
    log.PanicIf(err)

    if len(trackInfo) > 0 {
        ids := make([]string, len(trackInfo))

        for i, info := range trackInfo {
            ids[i] = info.Id
        }

        mc := napster.NewMetadataClient(i.ctx, i.hc, i.napsterApiKey)
        tracks, err := mc.GetTrackDetail(ids...)
        log.PanicIf(err)

        for _, track := range tracks {
            nt := i.getNapsterNormalizedTrack(&track)

            // We're going to check a couple of different things and be 
            // discriminating in what we print. This should allow us to 
            // efficiently cherry-pick artists, maybe even one at a time, to 
            // add to the playlist.

            // If track is already in Spotify, don't do or print anything. 

            h := nt.Hash() 
            if _, found := i.spotifyIndex[h]; found == true {
                continue
            }

            // One of the artists on the track must be in the `onlyArtists` 
            // list. If track is *not* in Spotify and not in the `onlyArtists` 
            // list, skip and print. 
            //
            // Our complexity is higher because each track is associated with 
            // potentially more than one artist.

            found := false
            for _, anTrack := range nt.ArtistNames {
                for _, anAllowed := range onlyArtists {
                    if anAllowed == anTrack {
                        found = true
                        break
                    }
                }
            }

            if found == false {
                skipped++

                for _, an := range nt.ArtistNames {
                    i.artistNotices[an] = true
                }

                continue
            }

            // If track is not in Spotify and *in* the list, print and add.
            //
            // Note that this struct will only have exactly one artist (Napster only returns one). 

            spotifyTrackId, err := i.getSpotifyTrackIdWithNames(nt.ArtistNames[0], nt.AlbumName, nt.TrackName)
            if log.Is(err, ErrTrackNotFoundInSpotify) == true {
                missingPhrase := fmt.Sprintf("[%s] [%s] [%s]", nt.ArtistNames[0], nt.AlbumName, nt.TrackName)

                missing = append(missing, missingPhrase)
                iLog.Warningf(i.ctx, "NOT FOUND IN SPOTIFY: %s", missingPhrase)

                continue
            } else if err != nil {
                log.PanicIf(err)
            }

            iLog.Infof(i.ctx, "WILL ADD: [%s] [%s] [%s]", nt.ArtistNames[0], nt.AlbumName, nt.TrackName)

            collector.idList = append(collector.idList, spotifyTrackId)
        }
    }

    return len(trackInfo), skipped, missing, nil
}

func (i *Importer) readSpotifyPlaylist(playlistId spotify.ID, userId string) (tracks []*NormalizedTrack, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Debugf(i.ctx, "Reading Spotify playlist.")

    ptp, err := i.spotifyAuth.Client.GetPlaylistTracks(userId, playlistId)
    log.PanicIf(err)

    tracks = make([]*NormalizedTrack, len(ptp.Tracks))
    for j, pt := range ptp.Tracks {
        nt := i.getSpotifyNormalizedTrack(&pt.Track)
        tracks[j] = nt
    }

    return tracks, nil
}

func (i *Importer) buildSpotifyIndex(tracks []*NormalizedTrack) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Debugf(i.ctx, "Building index with existing songs.")

    for _, nt := range tracks {
        h := nt.Hash()
        i.spotifyIndex[h] = true
    }

    return nil
}

func (i *Importer) preloadExisting(spotifyPlaylistName string) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    spotifyUserId, err := i.sc.GetSpotifyCurrentUserId()
    log.PanicIf(err)

    spotifyPlaylistId, err := i.sc.GetSpotifyPlaylistId(spotifyUserId, spotifyPlaylistName)
    log.PanicIf(err)

    spotifyTracks, err := i.readSpotifyPlaylist(spotifyPlaylistId, spotifyUserId)
    log.PanicIf(err)

    err = i.buildSpotifyIndex(spotifyTracks)
    log.PanicIf(err)

    return nil
}


// trackCollector Keeps track of the tracks that need to be added. We're going 
// to minimize our requests.
type trackCollector struct {
    idList []spotify.ID
}

func (i *Importer) GetTracksToAdd(spotifyPlaylistName string, onlyArtists []string) (tracks []spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    // Make artists lower-case.
    for i, a := range onlyArtists {
        onlyArtists[i] = strings.ToLower(a)
    }

    if err := i.preloadExisting(spotifyPlaylistName); err != nil {
        log.Panic(err)
    }

    iLog.Infof(i.ctx, "Reading Napster favorites.")

    a := napster.NewAuthenticator(i.ctx, i.hc, i.napsterApiKey, i.napsterSecretKey)
    a.SetUserCredentials(i.napsterUsername, i.napsterPassword)

    collector := new(trackCollector)
    amc := napster.NewAuthenticatedMemberClient(i.ctx, i.hc, a)

    skipped := 0
    missing := make([]string, 0)

    for {
        added, currentSkipped, missingUpdated, err := i.importBatch(amc, onlyArtists, collector, missing)
        log.PanicIf(err)

        missing = missingUpdated
        skipped += currentSkipped

        if added == 0 {
            break
        }

        iLog.Debugf(i.ctx, "(%d) tracks received starting at index (%d).", added, i.offset)

        i.offset += added
    }

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

    len_ := len(collector.idList)

    iLog.Infof(i.ctx, "(%d) tracks found to import.", len_)
    iLog.Infof(i.ctx, "(%d) tracks skipped.", skipped)
    iLog.Infof(i.ctx, "(%d) tracks missing.", len(missing))

    for j, missingPhrase := range missing {
        iLog.Infof(i.ctx, "TRACK NOT FOUND: (%d) %s", j, missingPhrase)
    }

    return collector.idList, nil
}

func init() {
    var err error
    invalidTrackChars, err = regexp.Compile("[^a-zA-Z0-9' ]+")
    log.PanicIf(err)
}
