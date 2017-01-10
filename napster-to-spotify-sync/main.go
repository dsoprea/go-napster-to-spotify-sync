package main

import (
    "fmt"
    "os"

    "github.com/dsoprea/go-logging"

    "github.com/dsoprea/go-napster-to-spotify-sync/internal/sync"
)

const (
    RedirectUrl = "http://localhost:8888/authResponse"
    LocalBindUrl = ":8888"
)

// Config
var (
    ApiClientId = os.Getenv("SPOTIFY_CLIENT_ID")
    ApiSecretKey = os.Getenv("SPOTIFY_SECRET_KEY")
)

// Misc
var (
    mLog = log.NewLogger("main")
)

func main() {
    nss := gnsssync.NewNapsterSpotifySync(ApiClientId, ApiSecretKey, RedirectUrl, LocalBindUrl)
    if err := nss.Sync(); err != nil {
        log.Panic(err)
    }

    mLog.Infof(nil, "Sync complete.")
}

func init() {
    if ApiClientId == "" {
        log.Panic(fmt.Errorf("client-ID is empty"))
    }
    
    if ApiSecretKey == "" {
        log.Panic(fmt.Errorf("secret-key is empty"))
    }
}
