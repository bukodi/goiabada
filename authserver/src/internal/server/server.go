package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/gorilla/csrf"
	"github.com/gorilla/securecookie"
	"github.com/leodip/goiabada/internal/common"
	core_token "github.com/leodip/goiabada/internal/core/token"

	"github.com/leodip/goiabada/internal/data"
	"github.com/leodip/goiabada/internal/entities"
	"github.com/leodip/goiabada/internal/sessionstore"
	"github.com/spf13/viper"
)

type Server struct {
	router       *chi.Mux
	database     *data.Database
	sessionStore *sessionstore.MySQLStore
	tokenParser  *core_token.TokenParser
}

func NewServer(router *chi.Mux, database *data.Database, sessionStore *sessionstore.MySQLStore) *Server {

	return &Server{
		router:       router,
		database:     database,
		sessionStore: sessionStore,
		tokenParser:  core_token.NewTokenParser(database),
	}
}

func (s *Server) Start(settings *entities.Settings) {
	s.initMiddleware(settings)

	staticDir := viper.GetString("StaticDir")
	slog.Info(fmt.Sprintf("using static files directory %v", staticDir))
	s.serveStaticFiles("/static", http.Dir(staticDir))

	s.initRoutes()
	certFile := viper.GetString("CertFile")
	keyFile := viper.GetString("KeyFile")

	slog.Info(fmt.Sprintf("cert file: %v", certFile))
	slog.Info(fmt.Sprintf("key file: %v", keyFile))
	slog.Info(fmt.Sprintf("starting to listen on port %v (https)", viper.GetString("Port")))
	log.Fatal(http.ListenAndServeTLS(fmt.Sprintf(":%v", viper.GetString("Port")), certFile, keyFile, s.router))
}

func (s *Server) initMiddleware(settings *entities.Settings) {
	// configures CORS
	s.router.Use(cors.Handler(cors.Options{
		AllowOriginFunc: func(r *http.Request, origin string) bool {
			if r.URL.Path == "/.well-known/openid-configuration" {
				// always allow the discovery URL
				return true
			} else if r.URL.Path == "/auth/token" || r.URL.Path == "/userinfo" {
				// allow when the web origin of the request matches a web origin in the database
				webOrigins, err := s.database.GetAllWebOrigins()
				if err != nil {
					slog.Error(err.Error())
					return false
				}
				for _, or := range webOrigins {
					if or.Origin == origin {
						return true
					}
				}
			}
			return false
		},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
		Debug:          true,
	}))

	s.router.Use(middleware.RequestID)
	if viper.GetBool("IsBehindAReverseProxy") {
		s.router.Use(middleware.RealIP)
	}

	httpRequestLoggingEnabled := viper.GetBool("Logger.Router.HttpRequests.Enabled")
	if httpRequestLoggingEnabled {
		slog.Info("http request logging enabled")
		s.router.Use(middleware.Logger)
	}
	s.router.Use(middleware.StripSlashes)
	s.router.Use(middleware.Timeout(60 * time.Second))

	// skips csrf for certain routes
	s.router.Use(func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			skip := false
			if strings.HasPrefix(r.URL.Path, "/static") ||
				strings.HasPrefix(r.URL.Path, "/userinfo") ||
				strings.HasPrefix(r.URL.Path, "/auth/token") ||
				strings.HasPrefix(r.URL.Path, "/auth/callback") {
				skip = true
			}
			if skip {
				r = csrf.UnsafeSkipCheck(r)
			}
			next.ServeHTTP(w, r.WithContext(r.Context()))
		}
		return http.HandlerFunc(fn)
	})

	s.router.Use(csrf.Protect(settings.SessionAuthenticationKey))

	// injects the application settings in the request context
	s.router.Use(func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			settings, err := s.database.GetSettings()
			if err != nil {
				slog.Error(strings.TrimSpace(err.Error()), "request-id", middleware.GetReqID(r.Context()))
				http.Error(w, fmt.Sprintf("fatal failure in GetSettings() middleware. For additional information, refer to the server logs. Request Id: %v", middleware.GetReqID(r.Context())), http.StatusInternalServerError)
			} else {
				ctx = context.WithValue(ctx, common.ContextKeySettings, settings)
				next.ServeHTTP(w, r.WithContext(ctx))
			}
		}
		return http.HandlerFunc(fn)
	})

	// clear the session cookie and redirect if unable to decode it
	s.router.Use(func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			_, err := s.sessionStore.Get(r, common.SessionName)
			if err != nil {
				multiErr, ok := err.(securecookie.MultiError)
				if ok && multiErr.IsDecode() {
					cookie := http.Cookie{
						Name:    common.SessionName,
						Expires: time.Now().AddDate(0, 0, -1),
						MaxAge:  -1,
						Path:    "/",
					}
					http.SetCookie(w, &cookie)
					http.Redirect(w, r, r.RequestURI, http.StatusFound)
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		}
		return http.HandlerFunc(fn)
	})

	// adds the session identifier (if available) to the request context
	s.router.Use(func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			requestId := middleware.GetReqID(ctx)

			errorMsg := fmt.Sprintf("fatal failure in session middleware. For additional information, refer to the server logs. Request Id: %v", requestId)

			sess, err := s.sessionStore.Get(r, common.SessionName)
			if err != nil {
				slog.Error(fmt.Sprintf("unable to get the session store: %v", err.Error()), "request-id", requestId)
				http.Error(w, errorMsg, http.StatusInternalServerError)
				return
			}

			if sess.Values[common.SessionKeySessionIdentifier] != nil {
				sessionIdentifier := sess.Values[common.SessionKeySessionIdentifier].(string)

				userSession, err := s.database.GetUserSessionBySessionIdentifier(sessionIdentifier)
				if err != nil {
					slog.Error(fmt.Sprintf("unable to get the user session: %v", err.Error()), "request-id", requestId)
					http.Error(w, errorMsg, http.StatusInternalServerError)
					return
				}
				if userSession == nil {
					// session has been deleted, will clear the session state
					sess.Values = make(map[interface{}]interface{})
					err = sess.Save(r, w)
					if err != nil {
						slog.Error(fmt.Sprintf("unable to save the session: %v", err.Error()), "request-id", requestId)
						http.Error(w, errorMsg, http.StatusInternalServerError)
						return
					}
				} else {
					ctx = context.WithValue(ctx, common.ContextKeySessionIdentifier, sessionIdentifier)
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		}
		return http.HandlerFunc(fn)
	})
}

func (s *Server) serveStaticFiles(path string, root http.FileSystem) {

	if path != "/" && path[len(path)-1] != '/' {
		s.router.Get(path, http.RedirectHandler(path+"/", http.StatusMovedPermanently).ServeHTTP)
		path += "/"
	}
	path += "*"

	s.router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fs := http.StripPrefix(pathPrefix, http.FileServer(root))

		cacheInSeconds := 5 * 60
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%v", cacheInSeconds))
		w.Header().Set("Expires", time.Now().Add(time.Second*time.Duration(cacheInSeconds)).Format(http.TimeFormat))
		w.Header().Set("Vary", "Accept-Encoding")

		fs.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndexGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Welcome to goiabada!"))
	}
}
