package gnsssync

import (
    "fmt"

    "net/http"

    "golang.org/x/net/context"

    "github.com/pkg/browser"
    "github.com/zmb3/spotify"
    "github.com/gorilla/mux"
    "github.com/dsoprea/go-logging"
)

// Config
const (
    staticStateString = "arbitrary-state-data"
)

// Errors
var (
    ErrImportComplete = fmt.Errorf("import complete")
)

// Misc
var (
    saLog = log.NewLogger("gnss.spotify_authorizer")
)


type SpotifyAuthorizer struct {
    ctx context.Context

    apiClientId string
    apiSecretKey string
    apiRedirectUrl string
    localBindUrl string
    authC chan<- *SpotifyContext

    auth spotify.Authenticator
}

func NewSpotifyAuthorizer(ctx context.Context, apiClientId, apiSecretKey, redirectUrl, localBindUrl string, authC chan<- *SpotifyContext) *SpotifyAuthorizer {
    return &SpotifyAuthorizer{
        ctx: ctx,
        apiClientId: apiClientId,
        apiSecretKey: apiSecretKey,
        apiRedirectUrl: redirectUrl,
        localBindUrl: localBindUrl,
        authC: authC,
    }
}


type SpotifyContext struct {
    Sa spotify.Authenticator
    Client spotify.Client
}

func (sa *SpotifyAuthorizer) handleResponse(w http.ResponseWriter, r *http.Request) {
    authCode := r.FormValue("code")
    if authCode == "" {
        log.Panic(fmt.Errorf("no auth"))
    }

    w.WriteHeader(http.StatusOK)
    fmt.Fprintf(w, "Success")

    t, err := sa.auth.Token(staticStateString, r)
    log.PanicIf(err)

    c := sa.auth.NewClient(t)

    sc := &SpotifyContext{
        Sa: sa.auth,
        Client: c,
    }

    sa.authC <- sc

    saLog.Debugf(sa.ctx, "Authorization is complete.")
}

func (sa *SpotifyAuthorizer) configureHttp() (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    saLog.Debugf(nil, "Starting web-server.")

    r := mux.NewRouter()
    r.HandleFunc("/authResponse", sa.handleResponse)

    if err := http.ListenAndServe(sa.localBindUrl, r); err != nil {
        log.Panic(err)
    }

    return nil
}

func (sa *SpotifyAuthorizer) Authorize() (err error) {
    defer func() {
        if state := recover(); state != nil {
            err = state.(error)
        }
    }()

    scopes := []string {
        spotify.ScopeUserReadPrivate,
        spotify.ScopePlaylistReadCollaborative,
        spotify.ScopePlaylistReadPrivate,
        spotify.ScopePlaylistModifyPrivate,
        spotify.ScopePlaylistModifyPublic,
    }

    // the redirect URL must be an exact match of a URL you've registered for your application
    // scopes determine which permissions the user is prompted to authorize
    sa.auth = spotify.NewAuthenticator(sa.apiRedirectUrl, scopes...)

    // if you didn't store your ID and secret key in the specified environment variables,
    // you can set them manually here
    sa.auth.SetAuthInfo(sa.apiClientId, sa.apiSecretKey)

    // get the user to this URL - how you do that is up to you
    // you should specify a unique state string to identify the session
    url := sa.auth.AuthURL(staticStateString)

    // Open the browser.

    saLog.Debugf(nil, "Opening: [%s]", url)

    if err := browser.OpenURL(url); err != nil {
        log.Panic(err)
    }

    // Wait for the response.
    if err := sa.configureHttp(); err != nil {
        log.Panic(err)
    }

    return nil
}
