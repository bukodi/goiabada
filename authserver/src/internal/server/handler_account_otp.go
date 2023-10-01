package server

import (
	"net/http"

	"github.com/gorilla/csrf"
	"github.com/leodip/goiabada/internal/common"
	"github.com/leodip/goiabada/internal/dtos"
	"github.com/leodip/goiabada/internal/entities"
	"github.com/leodip/goiabada/internal/enums"
	"github.com/leodip/goiabada/internal/lib"
	"github.com/pquerna/otp/totp"
)

func (s *Server) handleAccountOtpGet(otpSecretGenerator otpSecretGenerator) http.HandlerFunc {

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

		bind := map[string]interface{}{
			"otpEnabled": user.OTPEnabled,
			"csrfField":  csrf.TemplateField(r),
		}

		if !user.OTPEnabled {
			// generate secret
			settings := r.Context().Value(common.ContextKeySettings).(*entities.Settings)
			base64Image, secretKey, err := otpSecretGenerator.GenerateOTPSecret(user, settings)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}
			bind["base64Image"] = base64Image
			bind["secretKey"] = secretKey

			sess, err := s.sessionStore.Get(r, common.SessionName)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}

			// save image and secret in the session state
			sess.Values[common.SessionKeyOTPSecret] = secretKey
			sess.Values[common.SessionKeyOTPImage] = base64Image
			err = sess.Save(r, w)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}
		}

		err = s.renderTemplate(w, r, "/layouts/account_layout.html", "/account_otp.html", bind)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
	}
}

func (s *Server) handleAccountOtpPost() http.HandlerFunc {

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

		password := r.FormValue("password")

		renderError := func(message string, base64Image string, secretKey string) error {
			bind := map[string]interface{}{
				"error":      message,
				"otpEnabled": user.OTPEnabled,
				"csrfField":  csrf.TemplateField(r),
			}

			if len(base64Image) > 0 {
				bind["base64Image"] = base64Image
				bind["secretKey"] = secretKey
			}

			err := s.renderTemplate(w, r, "/layouts/account_layout.html", "/account_otp.html", bind)
			if err != nil {
				return err
			}
			return nil
		}

		const authFailedError = "Authentication failed. Check your password and try again."

		if user.OTPEnabled {
			// disable OTP

			if !lib.VerifyPasswordHash(user.PasswordHash, password) {
				if err := renderError(authFailedError, "", ""); err != nil {
					s.internalServerError(w, r, err)
					return
				}
				return
			}

			// disable OTP
			user.OTPSecret = ""
			user.OTPEnabled = false
			user, err = s.database.UpdateUser(user)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}
		} else {
			// enable OTP

			sess, err := s.sessionStore.Get(r, common.SessionName)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}

			base64Image, secretKey := "", ""
			if val, ok := sess.Values[common.SessionKeyOTPImage]; ok {
				base64Image = val.(string)
			}
			if val, ok := sess.Values[common.SessionKeyOTPSecret]; ok {
				secretKey = val.(string)
			}

			if !lib.VerifyPasswordHash(user.PasswordHash, password) {
				if err := renderError(authFailedError, base64Image, secretKey); err != nil {
					s.internalServerError(w, r, err)
					return
				}
				return
			}

			otpCode := r.FormValue("otp")
			if len(otpCode) == 0 {
				if err := renderError("OTP code is required.", base64Image, secretKey); err != nil {
					s.internalServerError(w, r, err)
					return
				}
				return
			}

			otpValid := totp.Validate(otpCode, secretKey)
			if !otpValid {
				if err := renderError("Incorrect OTP Code. OTP codes are time-sensitive and change every 30 seconds. Make sure you're using the most recent code generated by your authenticator app.", base64Image, secretKey); err != nil {
					s.internalServerError(w, r, err)
					return
				}
				return
			}

			// save OTP secret
			user.OTPSecret = secretKey
			user.OTPEnabled = true
			user, err = s.database.UpdateUser(user)
			if err != nil {
				s.internalServerError(w, r, err)
				return
			}
		}

		http.Redirect(w, r, "/account/otp", http.StatusFound)
	}
}