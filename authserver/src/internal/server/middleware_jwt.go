package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/leodip/goiabada/internal/common"
	core_validators "github.com/leodip/goiabada/internal/core/validators"
	"github.com/leodip/goiabada/internal/dtos"
	"github.com/leodip/goiabada/internal/lib"
	"github.com/leodip/goiabada/internal/sessionstore"
)

func MiddlewareJwtSessionToContext(next http.Handler, sessionStore *sessionstore.MySQLStore,
	tokenValidator *core_validators.TokenValidator) http.HandlerFunc {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		sess, err := sessionStore.Get(r, common.SessionName)
		if err != nil {
			http.Error(w, fmt.Sprintf("unable to get the session in JwtSessionToContext middleware: %v", err.Error()), http.StatusInternalServerError)
			return
		}

		if sess.Values[common.SessionKeyJwt] != nil {
			tokenResponse, ok := sess.Values[common.SessionKeyJwt].(dtos.TokenResponse)
			if !ok {
				http.Error(w, "unable to cast the session value to TokenResponse in JwtSessionToContext middleware", http.StatusInternalServerError)
				return
			}
			jwtInfo, err := tokenValidator.ValidateJwtSignature(r.Context(), &tokenResponse)
			if err == nil {
				ctx = context.WithValue(ctx, common.ContextKeyJwtInfo, *jwtInfo)
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func MiddlewareRequiresScope(next http.Handler, server *Server, clientIdentifier string,
	scopesAnyOf []string) http.HandlerFunc {

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		var jwtInfo dtos.JwtInfo
		var ok bool
		if r.Context().Value(common.ContextKeyJwtInfo) != nil {
			jwtInfo, ok = r.Context().Value(common.ContextKeyJwtInfo).(dtos.JwtInfo)
			if !ok {
				http.Error(w, "unable to cast the context value to JwtInfo in WithAuthorization middleware", http.StatusInternalServerError)
				return
			}
		}

		isAuthorized := server.isAuthorizedToAccessResource(jwtInfo, scopesAnyOf)

		// Ajax request?
		if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
			if !isAuthorized {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
		} else {
			sess, err := server.sessionStore.Get(r, common.SessionName)
			if err != nil {
				http.Error(w, fmt.Sprintf("unable to get the session in WithAuthorization middleware: %v", err.Error()), http.StatusInternalServerError)
				return
			}

			if !isAuthorized {
				var redirectCount int
				if sess.Values[common.SessionKeyRedirToAuthorizeCount] != nil {
					redirectCount, ok = sess.Values[common.SessionKeyRedirToAuthorizeCount].(int)
					if !ok {
						http.Error(w, "unable to cast the session value (SessionKeyRedirToAuthorizeCount) to int in WithAuthorization middleware", http.StatusInternalServerError)
						return
					}
					redirectCount++
				} else {
					redirectCount = 1
				}
				sess.Values[common.SessionKeyRedirToAuthorizeCount] = redirectCount

				if redirectCount > 2 {
					// reset the counter
					delete(sess.Values, common.SessionKeyRedirToAuthorizeCount)
					err = sess.Save(r, w)
					if err != nil {
						http.Error(w, fmt.Sprintf("unable to save the session in WithAuthorization middleware: %v", err.Error()), http.StatusInternalServerError)
						return
					}

					// prevent infinite loop
					server.handleUnauthorizedGet()(w, r)
					return
				}

				server.redirToAuthorize(w, r, "system-website", lib.GetBaseUrl()+r.RequestURI)
				return
			} else {
				// reset the counter
				delete(sess.Values, common.SessionKeyRedirToAuthorizeCount)
				err = sess.Save(r, w)
				if err != nil {
					http.Error(w, fmt.Sprintf("unable to save the session in WithAuthorization middleware: %v", err.Error()), http.StatusInternalServerError)
					return
				}
			}
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
