package gnsssync

import (
    "fmt"
    "strings"
    "sort"

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

// getSpotifyTrackId Find and add the track to the Spotify playlist.
func (i *Importer) getSpotifyTrackId(nnt *NormalizedTrack) (id spotify.ID, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Debugf(i.ctx, "Finding: [%s]", nnt)

    var sr *spotify.SearchResult

    trackHits := 0
    albumHits := 0

    for {
        if sr == nil {
            iLog.Debugf(i.ctx, "Searching for track: [%s]", nnt.TrackName)
            sr, err = i.spotifyAuth.Client.Search(nnt.TrackName, spotify.SearchTypeTrack)
            log.PanicIf(err)
        } else if err := i.spotifyAuth.Client.NextTrackResults(sr); err == spotify.ErrNoMorePages {
            break
        } else if err != nil {
            iLog.Debugf(i.ctx, "(Retrieving next page of results.)")
            log.Panic(err)
        }

        choices := make([]string, len(sr.Tracks.Tracks))
        for j, ft := range sr.Tracks.Tracks {
            snt := i.getSpotifyNormalizedTrack(&ft)

            choices[j] = snt.String()

            // These should probably all be very similar, as this was a track-based 
            // search.
            if snt.TrackName != nnt.TrackName {
                iLog.Debugf(i.ctx, "One track result does not match: [%s] != [%s]", snt.TrackName, nnt.TrackName)
                continue
            } else {
                trackHits++
                iLog.Debugf(i.ctx, "Matched track: [%s]", nnt.TrackName)
            }

            if snt.AlbumName != nnt.AlbumName {
                iLog.Debugf(i.ctx, "One incorrect album candidate for matched track: [%s] != [%s]", snt.AlbumName, nnt.AlbumName)
                continue
            } else {
                albumHits++
                iLog.Debugf(i.ctx, "Matched album: [%s]", nnt.AlbumName)
            }

            // Look for an intersection between the artist we want and the list of 
            // artists associated with the song.
            //
            // Note that Napster only produces one artist.
            for _, an := range snt.ArtistNames {
                napsterArtistName := nnt.ArtistNames[0]
                if an != napsterArtistName {
                    iLog.Debugf(i.ctx, "One incorrect artist candidate for matched track and album: [%s] != [%s]", an, napsterArtistName)
                } else {
                    return ft.ID, nil
                }
            }
        }

        iLog.Warningf(i.ctx, "Choices: %v", choices)
    }

    if albumHits > 0 {
        iLog.Warningf(i.ctx, "No matching albums not found: [%s]", nnt.AlbumName)
    } else if trackHits > 0 {
        iLog.Warningf(i.ctx, "No matching tracks for matching albums found: [%s]", nnt.TrackName)
    } else {
        iLog.Warningf(i.ctx, "No matching artists for matching tracks and albums found: [%s]", nnt.ArtistNames[0])
    }

    log.Panic(ErrTrackNotFoundInSpotify)

    // Obligatory.
    return spotify.ID(""), nil
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

func (i *Importer) importBatch(amc *napster.AuthenticatedMemberClient, onlyArtists []string, collector *trackCollector) (count int, skipped int, missing int, err error) {
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

            spotifyTrackId, err := i.getSpotifyTrackId(nt)
            if log.Is(err, ErrTrackNotFoundInSpotify) == true {
                missing++
                iLog.Warningf(i.ctx, "NOT FOUND IN SPOTIFY: [%s] [%s] [%s]", nt.ArtistNames[0], nt.AlbumName, nt.TrackName)

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

    skipped := 0
    missing := 0

    for {
        added, currentSkipped, currentMissing, err := i.importBatch(amc, onlyArtists, collector)
        log.PanicIf(err)

        skipped += currentSkipped
        missing += currentMissing

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
    iLog.Infof(i.ctx, "(%d) tracks missing.", missing)

    if len_ == 0 {
        iLog.Warningf(i.ctx, "No tracks found to import.")
    } else {
        iLog.Infof(i.ctx, "Adding tracks to the playlist.")

        iLog.Infof(i.ctx, "SKIPPING ADD")
        spotifyUserId = spotifyUserId
        spotifyPlaylistId = spotifyPlaylistId

        _, err := i.spotifyAuth.Client.AddTracksToPlaylist(spotifyUserId, spotifyPlaylistId, collector.idList...)
        log.PanicIf(err)
    }

    return nil
}
