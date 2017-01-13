package main

import (
    "fmt"
    "os"

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
    SpotifyApiClientId = os.Getenv("SPOTIFY_CLIENT_ID")
    SpotifyApiSecretKey = os.Getenv("SPOTIFY_SECRET_KEY")

    NapsterApiKey = os.Getenv("NAPSTER_API_KEY")
    NapsterSecretKey = os.Getenv("NAPSTER_SECRET_KEY")
    NapsterUsername = os.Getenv("NAPSTER_USERNAME")
    NapsterPassword = os.Getenv("NAPSTER_PASSWORD")

    ImportBatchSize = 100
)

// Misc
var (
    mLog = log.NewLogger("main")
)


type options struct {
    SpotifyPlaylistName string  `short:"p" long:"playlist-name" description:"Spotify playlist name" required:"true"`
    OnlyArtists []string        `short:"a" long:"only-artists" description:"One artist to import" required:"true"`
}

func readOptions() *options {
    o := new(options)

    if _, err := flags.Parse(o); err != nil {
        log.Panic(err)
    }

    return o
}

func main() {
    ecp := log.NewEnvironmentConfigurationProvider()
    log.LoadConfiguration(ecp)

    cla := log.NewConsoleLogAdapter()
    log.AddAdapter("console", cla)

    log.AddExcludeFilter("napster.client")
    log.AddExcludeFilter("napster.authorization")

    o := readOptions()
    ctx := context.Background()
    authC := make(chan *gnsssync.SpotifyContext)

    go func() {
        sa := gnsssync.NewSpotifyAuthorizer(ctx, SpotifyApiClientId, SpotifyApiSecretKey, SpotifyRedirectUrl, SpotifyAuthorizeLocalBindUrl, authC)
        if err := sa.Authorize(); err != nil {
            log.Panic(err)
        }

        // Somehow the HTTP handler doesn't hold the application open and we'll 
        // terminate at the end as would be desired.
    }()

    doneC := make(chan bool)

    go func() {
        spotifyAuth := <-authC

        mLog.Debugf(nil, "Received auth-code. Proceeding with import.")

        i := gnsssync.NewImporter(ctx, NapsterApiKey, NapsterSecretKey, NapsterUsername, NapsterPassword, spotifyAuth, SpotifyApiSecretKey, ImportBatchSize)
        if err := i.Import(o.SpotifyPlaylistName, o.OnlyArtists); err != nil {
            log.Panic(err)
        }

        doneC <- true
    }()

    <-doneC
}

func init() {
    if SpotifyApiClientId == "" {
        log.Panic(fmt.Errorf("Spotify client-ID is empty"))
    }
    
    if SpotifyApiSecretKey == "" {
        log.Panic(fmt.Errorf("Spotify secret-key is empty"))
    }
}
