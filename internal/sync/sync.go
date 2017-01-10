package gnsssync

import (
    "fmt"

    "net/http"

    "github.com/pkg/browser"
    "github.com/zmb3/spotify"
    "github.com/gorilla/mux"
    "github.com/dsoprea/go-logging"
)

// Misc
var (
    sLog = log.NewLogger("gnss.sync")
)


type NapsterSpotifySync struct {
    apiClientId string
    apiSecretKey string
    apiRedirectUrl string
    localBindUrl string
}

func NewNapsterSpotifySync(apiClientId, apiSecretKey, redirectUrl, localBindUrl string) *NapsterSpotifySync {
    return &NapsterSpotifySync{
        apiClientId: apiClientId,
        apiSecretKey: apiSecretKey,
        apiRedirectUrl: redirectUrl,
        localBindUrl: localBindUrl,
    }
}

func (nss *NapsterSpotifySync) haveAuth(authCode string) (err error) {
    fmt.Printf("Auth: [%s]", authCode)

// TODO(dustin): !! Finish.

    return nil
}

func (nss *NapsterSpotifySync) handleResponse(w http.ResponseWriter, r *http.Request) {
    authCode := r.FormValue("code")
    if authCode == "" {
        log.Panic(fmt.Errorf("no auth"))
    }

    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Success")

    if err := nss.haveAuth(authCode); err != nil {
        log.Panic(err)
    }
}

func (nss *NapsterSpotifySync) configureHttp() {
    sLog.Debugf(nil, "Starting web-server.")

    r := mux.NewRouter()
    r.HandleFunc("/authResponse", nss.handleResponse)

    if err := http.ListenAndServe(nss.localBindUrl, r); err != nil {
        log.Panic(err)
    }
}

func (nss *NapsterSpotifySync) Sync() (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    // the redirect URL must be an exact match of a URL you've registered for your application
    // scopes determine which permissions the user is prompted to authorize
    auth := spotify.NewAuthenticator(nss.apiRedirectUrl, spotify.ScopeUserReadPrivate)

    // if you didn't store your ID and secret key in the specified environment variables,
    // you can set them manually here
    auth.SetAuthInfo(nss.apiClientId, nss.apiSecretKey)

    // get the user to this URL - how you do that is up to you
    // you should specify a unique state string to identify the session
    stateString := "arbitrary-state-data"
    url := auth.AuthURL(stateString)

    // Open the browser.

    sLog.Debugf(nil, "Opening: [%s]", url)

    if err := browser.OpenURL(url); err != nil {
        log.Panic(err)
    }

    // Wait for the response.

    nss.configureHttp()

    return nil
}
