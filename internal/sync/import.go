package gnsssync

import (
    "fmt"

    "net/http"

    "golang.org/x/net/context"

    "github.com/dsoprea/go-logging"
    "github.com/dsoprea/go-napster"
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
}

func NewImporter(ctx context.Context, napsterApiKey, napsterSecretKey, napsterUsername, napsterPassword string, spotifyAuth *SpotifyContext, spotifyApiSecretKey string, batchSize int) *Importer {
    hc := new(http.Client)

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
    }
}

// importTrack Find and add the track to the Spotify playlist.
func (i *Importer) importTrack(amc *napster.AuthenticatedMemberClient, track *napster.MetadataTrackDetail) (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    fmt.Printf("Track: [%s] [%s] [%s]\n", track.ArtistName, track.AlbumName, track.Name)

// TODO(dustin): !! Finish.

    return nil
}

func (i *Importer) importBatch(amc *napster.AuthenticatedMemberClient) (count int, err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    fmt.Printf("Fetching from index (%d).\n", i.offset)

    trackInfo, err := amc.GetFavoriteTracks(i.offset, i.batchSize)
    log.PanicIf(err)

    if len(trackInfo) > 0 {
        ids := make([]string, len(trackInfo))

        for i, info := range trackInfo {
            ids[i] = info.Id
        }

        iLog.Infof(nil, "Retrieving track details.")

        mc := napster.NewMetadataClient(i.ctx, i.hc, i.napsterApiKey)
        tracks, err := mc.GetTrackDetail(ids...)
        log.PanicIf(err)

        for _, track := range tracks {
            if err := i.importTrack(amc, &track); err != nil {
                log.Panic(err)
            }
        }
    }

    return len(trackInfo), nil
}

func (i *Importer) Import() (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    iLog.Infof(nil, "Reading napster favorites.")

    a := napster.NewAuthenticator(i.ctx, i.hc, i.napsterApiKey, i.napsterSecretKey)
    a.SetUserCredentials(i.napsterUsername, i.napsterPassword)

    amc := napster.NewAuthenticatedMemberClient(i.ctx, i.hc, a)

    for {
        count, err := i.importBatch(amc)
        log.PanicIf(err)

        iLog.Debugf(nil, "(%d) tracks received starting at index (%d).\n", count, i.offset)

        if count == 0 {
            break
        }

        i.offset += count
    }

    return nil
}
