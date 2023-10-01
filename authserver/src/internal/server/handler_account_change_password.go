package server

import (
	"net/http"
	"strings"

	"github.com/gorilla/csrf"
	"github.com/leodip/goiabada/internal/common"
	"github.com/leodip/goiabada/internal/dtos"
	"github.com/leodip/goiabada/internal/enums"
	"github.com/leodip/goiabada/internal/lib"
)

func (s *Server) handleAccountChangePasswordGet() http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		requiresAuth := true

		var jwtInfo dtos.JwtInfo
		if r.Context().Value(common.ContextKeyJwtInfo) != nil {
			jwtInfo = r.Context().Value(common.ContextKeyJwtInfo).(dtos.JwtInfo)
			acrLevel := jwtInfo.GetIdTokenAcrLevel()
			if acrLevel != nil && (*acrLevel == enums.AcrLevel2 || *acrLevel == enums.AcrLevel3) {
				requiresAuth = false
			}
		}

		if requiresAuth {
			s.redirToAuthorize(w, r, "account-management", r.RequestURI)
			return
		}

		bind := map[string]interface{}{
			"csrfField": csrf.TemplateField(r),
		}

		err := s.renderTemplate(w, r, "/layouts/account_layout.html", "/account_change_password.html", bind)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}

	}
}

func (s *Server) handleAccountChangePasswordPost(passwordValidator passwordValidator) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		requiresAuth := true

		var jwtInfo dtos.JwtInfo
		if r.Context().Value(common.ContextKeyJwtInfo) != nil {
			jwtInfo = r.Context().Value(common.ContextKeyJwtInfo).(dtos.JwtInfo)
			acrLevel := jwtInfo.GetIdTokenAcrLevel()
			if acrLevel != nil && (*acrLevel == enums.AcrLevel2 || *acrLevel == enums.AcrLevel3) {
				requiresAuth = false
			}
		}

		if requiresAuth {
			s.redirToAuthorize(w, r, "account-management", r.RequestURI)
			return
		}

		currentPassword := r.FormValue("currentPassword")
		newPassword := r.FormValue("newPassword")
		newPasswordConfirmation := r.FormValue("newPasswordConfirmation")

		renderError := func(message string) error {
			bind := map[string]interface{}{
				"error":     message,
				"csrfField": csrf.TemplateField(r),
			}

			err := s.renderTemplate(w, r, "/layouts/account_layout.html", "/account_change_password.html", bind)
			if err != nil {
				return err
			}
			return nil
		}

		if len(strings.TrimSpace(currentPassword)) == 0 {
			if err := renderError("Current password is required."); err != nil {
				s.internalServerError(w, r, err)
				return
			}
			return
		}

		sub, err := jwtInfo.IdTokenClaims.GetSubject()
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		user, err := s.database.GetUserBySubject(sub)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}

		if !lib.VerifyPasswordHash(user.PasswordHash, currentPassword) {
			if err := renderError("Authentication failed. Check your current password and try again."); err != nil {
				s.internalServerError(w, r, err)
				return
			}
			return
		}

		if len(strings.TrimSpace(newPassword)) == 0 {
			if err := renderError("New password is required."); err != nil {
				s.internalServerError(w, r, err)
				return
			}
			return
		}

		if newPassword != newPasswordConfirmation {
			if err := renderError("The new password confirmation does not match the password."); err != nil {
				s.internalServerError(w, r, err)
				return
			}
			return
		}

		err = passwordValidator.ValidatePassword(r.Context(), newPassword)
		if err != nil {
			if err := renderError(err.Error()); err != nil {
				s.internalServerError(w, r, err)
				return
			}
			return
		}

		passwordHash, err := lib.HashPassword(newPassword)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		user.PasswordHash = passwordHash
		user.ForgotPasswordCodeEncrypted = nil
		user.ForgotPasswordCodeIssuedAt = nil
		_, err = s.database.UpdateUser(user)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}

		bind := map[string]interface{}{
			"passwordChangedSuccessfully": true,
			"csrfField":                   csrf.TemplateField(r),
		}

		err = s.renderTemplate(w, r, "/layouts/account_layout.html", "/account_change_password.html", bind)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
	}
}