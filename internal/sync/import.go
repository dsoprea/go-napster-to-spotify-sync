package gnsssync

import (
    "fmt"
    "strings"

    "net/http"

    "golang.org/x/net/context"

    "github.com/dsoprea/go-logging"
    "github.com/dsoprea/go-napster"
    "github.com/zmb3/spotify"
    "github.com/randomingenuity/go-ri/common"
)

// Misc
var (
    iLog = log.NewLogger("gnss.import")
)


type Importer struct {
    ctx context.Context
    hc *http.Client

    napsterApiKey string
    napsterSecretKey string
    napsterUsername string
    napsterPassword string

    spotifyAuth *SpotifyContext
    spotifyApiSecretKey string

    batchSize int
    offset int

    spotifyIndex map[string]bool
    artistNotices map[string]bool
}

func NewImporter(ctx context.Context, napsterApiKey, napsterSecretKey, napsterUsername, napsterPassword string, spotifyAuth *SpotifyContext, spotifyApiSecretKey string, batchSize int) *Importer {
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
        spotifyApiSecretKey: spotifyApiSecretKey,

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

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (i *Importer) getSpotifyTrackId(nnt *NormalizedTrack) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    sr, err := i.spotifyAuth.Client.Search(nnt.TrackName, spotify.SearchTypeTrack)
    log.PanicIf(err)

    for _, ft := range sr.Tracks.Tracks {
        snt := i.getSpotifyNormalizedTrack(&ft)

        if snt.TrackName != nnt.TrackName {
            continue
        }

        if snt.AlbumName != nnt.AlbumName {
            continue
        }

        // Look for an intersection between the artist we want and the list of 
        // artists associated with the song.
        //
        // Note that Napster only produces one artist.
        for _, an := range snt.ArtistNames {
            if nnt.ArtistNames[0] == an {
                return ft.ID, nil
            }
        }
    }

    log.Panic(fmt.Errorf("track not found in Spotify: %v [%s] [%s]", nnt.ArtistNames, nnt.AlbumName, nnt.TrackName))

    // Obligatory.
    return spotify.ID(""), nil
}

func (i *Importer) getNapsterNormalizedTrack(track *napster.MetadataTrackDetail) *NormalizedTrack {
    return &NormalizedTrack{
        TrackName: track.Name,
        AlbumName: track.AlbumName,
        ArtistNames: []string { track.ArtistName },
    }
}

func (i *Importer) importBatch(amc *napster.AuthenticatedMemberClient, onlyArtists []string, collector *trackCollector) (count int, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    if len(onlyArtists) == 0 {
        log.Panic(fmt.Errorf("at least one artist must be given to import"))
    }

    fmt.Printf("Fetching from offset (%d).\n", i.offset)

    trackInfo, err := amc.GetFavoriteTracks(i.offset, i.batchSize)
    log.PanicIf(err)

    if len(trackInfo) > 0 {
        ids := make([]string, len(trackInfo))

        for i, info := range trackInfo {
            ids[i] = info.Id
        }

        iLog.Infof(i.ctx, "Retrieving track details.")

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
                for _, an := range nt.ArtistNames {
                    if _, found := i.artistNotices[an]; found == false {
                        iLog.Debugf(i.ctx, "One of the artists in the track that is being skipped: %v", an)
                        i.artistNotices[an] = true
                    }
                }

                continue
            }

            // If track is not in Spotify and *in* the list, print and add.
            //
            // Note that this struct will only have exactly one artist (Napster only returns one). 

            iLog.Infof(i.ctx, "Adding: [%s] [%s] [%s]\n", nt.ArtistNames[0], nt.AlbumName, nt.TrackName)

            spotifyTrackId, err := i.getSpotifyTrackId(nt)
            log.PanicIf(err)

            collector.idList = append(collector.idList, spotifyTrackId)
        }
    }

    return len(trackInfo), nil
}

func (i *Importer) getSpotifyPlaylistId(spotifyUserId string, playlistName string) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Debugf(i.ctx, "Getting playlist ID: [%s]", playlistName)

    splp, err := i.spotifyAuth.Client.GetPlaylistsForUser(spotifyUserId)
    log.PanicIf(err)

    playlistName = strings.ToLower(playlistName)
    for _, p := range splp.Playlists {
        currentPlaylistName := strings.ToLower(p.Name)
// TODO(dustin): !! Debugging.
iLog.Debugf(i.ctx, "Checking: [%s] == [%s]", currentPlaylistName, playlistName)

        if currentPlaylistName == playlistName {
            return p.ID, nil
        }
    }

    log.Panic(fmt.Errorf("playlist not found: [%s]", playlistName))

    // Obligatory.
    return spotify.ID(""), nil
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

func (i *Importer) getSpotifyCurrentUserId() (id string, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Debugf(i.ctx, "Getting current user ID.")

    pu, err := i.spotifyAuth.Client.CurrentUser()
    log.PanicIf(err)

    return pu.ID, nil
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

func (i *Importer) preloadExisting(spotifyPlaylistName string) (spotifyUserId string, spotifyPlaylistId spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    spotifyUserId, err = i.getSpotifyCurrentUserId()
    log.PanicIf(err)

    spotifyPlaylistId, err = i.getSpotifyPlaylistId(spotifyUserId, spotifyPlaylistName)
    log.PanicIf(err)

    spotifyTracks, err := i.readSpotifyPlaylist(spotifyPlaylistId, spotifyUserId)
    log.PanicIf(err)

    err = i.buildSpotifyIndex(spotifyTracks)
    log.PanicIf(err)

    return spotifyUserId, spotifyPlaylistId, nil
}


// trackCollector Keeps track of the tracks that need to be added. We're going 
// to minimize our requests.
type trackCollector struct {
    idList []spotify.ID
}

func (i *Importer) Import(spotifyPlaylistName string, onlyArtists []string) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    // Make artists lower-case.
    for i, a := range onlyArtists {
        onlyArtists[i] = strings.ToLower(a)
    }

    spotifyUserId, spotifyPlaylistId, err := i.preloadExisting(spotifyPlaylistName)
    log.PanicIf(err)

    iLog.Infof(i.ctx, "Reading Napster favorites.")

    a := napster.NewAuthenticator(i.ctx, i.hc, i.napsterApiKey, i.napsterSecretKey)
    a.SetUserCredentials(i.napsterUsername, i.napsterPassword)

    collector := new(trackCollector)
    amc := napster.NewAuthenticatedMemberClient(i.ctx, i.hc, a)

    for {
        count, err := i.importBatch(amc, onlyArtists, collector)
        log.PanicIf(err)

        if count == 0 {
            break
        }

        iLog.Debugf(i.ctx, "(%d) tracks received starting at index (%d).\n", count, i.offset)

        i.offset += count
    }

    iLog.Infof(i.ctx, "Adding (%d) tracks to the playlist.", len(collector.idList))

    iLog.Infof(i.ctx, "SKIPPING ADD")
    spotifyUserId = spotifyUserId
    spotifyPlaylistId = spotifyPlaylistId
/*
    _, err := i.spotifyAuth.Client.AddTracksToPlaylist(spotifyUserId, spotifyPlaylistId, collector.idList...)
    log.PanicIf(err)
*/
    return nil
}
