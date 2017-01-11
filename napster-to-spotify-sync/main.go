package main

import (
    "fmt"
    "os"

    "golang.org/x/net/context"

    "github.com/dsoprea/go-logging"

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

func main() {
    cla := log.NewConsoleLogAdapter()
    log.AddAdapter("console", cla)

    ecp := log.NewEnvironmentConfigurationProvider()
    log.LoadConfiguration(ecp)

// TODO(dustin): !! Logging is not working.

    ctx := context.Background()

    authC := make(chan *gnsssync.SpotifyContext)

    go func() {
        sa := gnsssync.NewSpotifyAuthorizer(SpotifyApiClientId, SpotifyApiSecretKey, SpotifyRedirectUrl, SpotifyAuthorizeLocalBindUrl, authC)
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
        if err := i.Import(); err != nil {
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
