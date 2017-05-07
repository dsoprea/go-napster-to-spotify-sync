package main

import (
    "os"
    "fmt"

    "golang.org/x/net/context"
    "github.com/dsoprea/go-logging"
    "github.com/jessevdk/go-flags"

    "github.com/dsoprea/go-napster-to-spotify-sync/internal/sync"
)

const (
    SpotifyRedirectUrl = "http://localhost:8888/authResponse"
    SpotifyAuthorizeLocalBindUrl = ":8888"
)

// Config
var (
    ImportBatchSize = 100
)

// Misc
var (
    mLog = log.NewLogger("main")
)

type options struct {
    SpotifyApiClientId string `long:"spotify-api-client-id" required:"true" description:"Spotify API client-ID"`
    SpotifyApiSecretKey string `long:"spotify-api-secret-key" required:"true" description:"Spotify API secret key"`

    NapsterApiKey string `long:"napster-api-key" required:"true" description:"Napster API key"`
    NapsterSecretKey string `long:"napster-secret-key" required:"true" description:"Napster secret key"`

    NapsterUsername string `long:"napster-username" required:"true" description:"Napster username"`
    NapsterPassword string `long:"napster-password" required:"true" description:"Napster password"`

    SpotifyPlaylistName string  `short:"p" long:"playlist-name" required:"true" description:"Spotify playlist name"`
    OnlyArtists []string        `short:"a" long:"only-artists" required:"true" description:"One artist to import"`

    NoChanges bool `short:"n" long:"no-changes" description:"Do not make changes to Spotify"`
}

func main() {
    ecp := log.NewEnvironmentConfigurationProvider()
    log.LoadConfiguration(ecp)

    cla := log.NewConsoleLogAdapter()
    log.AddAdapter("console", cla)

    log.AddExcludeFilter("napster.client")
    log.AddExcludeFilter("napster.authorization")

    o := new(options)
    if _, err := flags.Parse(o); err != nil {
        fmt.Println(err)
        os.Exit(1)
    }

    ctx := context.Background()
    authC := make(chan *gnsssync.SpotifyContext)

    go func() {
        sa := gnsssync.NewSpotifyAuthorizer(ctx, o.SpotifyApiClientId, o.SpotifyApiSecretKey, SpotifyRedirectUrl, SpotifyAuthorizeLocalBindUrl, authC)
        if err := sa.Authorize(); err != nil {
            log.Panic(err)
        }

        // Somehow the HTTP handler doesn't hold the application open and we'll 
        // terminate at the end as would be desired.
    }()

    spotifyAuth := <-authC

    mLog.Debugf(nil, "Received auth-code. Proceeding with import.")

    sc := gnsssync.NewSpotifyCache(ctx, spotifyAuth)
    i := gnsssync.NewImporter(ctx, o.NapsterApiKey, o.NapsterSecretKey, o.NapsterUsername, o.NapsterPassword, spotifyAuth, sc, ImportBatchSize)

    idList, err := i.GetTracksToAdd(o.SpotifyPlaylistName, o.OnlyArtists)
    log.PanicIf(err)

    len_ := len(idList)
    if len_ == 0 {
        mLog.Warningf(ctx, "No tracks found to import.")
    } else if o.NoChanges == true {
        mLog.Warningf(ctx, "There were changes to make but we were told to not make them.")
    } else {
        mLog.Infof(ctx, "Adding tracks to the playlist.")

        spotifyUserId, err := sc.GetSpotifyCurrentUserId()
        log.PanicIf(err)

        spotifyPlaylistId, err := sc.GetSpotifyPlaylistId(spotifyUserId, o.SpotifyPlaylistName)
        log.PanicIf(err)

        mLog.Infof(ctx, "Adding (%d) tracks.", len_)

        if _, err := spotifyAuth.Client.AddTracksToPlaylist(spotifyUserId, spotifyPlaylistId, idList...); err != nil {
            log.Panic(err)
        }
    }
}
