package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/csrf"
	"github.com/leodip/goiabada/internal/entities"
)

func (s *Server) handleAdminGroupAttributesAddGet() http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		idStr := chi.URLParam(r, "groupId")
		if len(idStr) == 0 {
			s.internalServerError(w, r, errors.New("groupId is required"))
			return
		}

		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		group, err := s.database.GetGroupById(uint(id))
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		if group == nil {
			s.internalServerError(w, r, errors.New("group not found"))
			return
		}

		bind := map[string]interface{}{
			"groupId":              group.Id,
			"groupIdentifier":      group.GroupIdentifier,
			"includeInAccessToken": true,
			"includeInIdToken":     true,
			"description":          group.Description,
			"csrfField":            csrf.TemplateField(r),
		}

		err = s.renderTemplate(w, r, "/layouts/menu_layout.html", "/admin_groups_attributes_add.html", bind)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
	}
}

func (s *Server) handleAdminGroupAttributesAddPost(identifierValidator identifierValidator,
	inputSanitizer inputSanitizer) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {

		idStr := chi.URLParam(r, "groupId")
		if len(idStr) == 0 {
			s.internalServerError(w, r, errors.New("groupId is required"))
			return
		}

		id, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		group, err := s.database.GetGroupById(uint(id))
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}
		if group == nil {
			s.internalServerError(w, r, errors.New("group not found"))
			return
		}

		renderError := func(message string) {
			bind := map[string]interface{}{
				"groupId":              group.Id,
				"groupIdentifier":      group.GroupIdentifier,
				"attributeKey":         r.FormValue("attributeKey"),
				"attributeValue":       r.FormValue("attributeValue"),
				"includeInAccessToken": r.FormValue("includeInAccessToken") == "on",
				"includeInIdToken":     r.FormValue("includeInIdToken") == "on",
				"error":                message,
				"csrfField":            csrf.TemplateField(r),
			}

			err := s.renderTemplate(w, r, "/layouts/menu_layout.html", "/admin_groups_attributes_add.html", bind)
			if err != nil {
				s.internalServerError(w, r, err)
			}
		}

		attrKey := r.FormValue("attributeKey")
		attrValue := r.FormValue("attributeValue")

		if len(attrKey) == 0 {
			renderError("Attribute key is required")
			return
		}

		err = identifierValidator.ValidateIdentifier(attrKey)
		if err != nil {
			renderError(err.Error())
			return
		}

		const maxLengthAttrValue = 250
		if len(attrValue) > maxLengthAttrValue {
			renderError("The attribute value cannot exceed a maximum length of " + strconv.Itoa(maxLengthAttrValue) + " characters. Please make the value shorter.")
			return
		}

		includeInAccessToken := r.FormValue("includeInAccessToken") == "on"
		includeInIdToken := r.FormValue("includeInIdToken") == "on"

		groupAttribute := &entities.GroupAttribute{
			Key:                  attrKey,
			Value:                attrValue,
			IncludeInAccessToken: includeInAccessToken,
			IncludeInIdToken:     includeInIdToken,
			GroupId:              group.Id,
		}
		_, err = s.database.CreateGroupAttribute(groupAttribute)
		if err != nil {
			s.internalServerError(w, r, err)
			return
		}

		http.Redirect(w, r, fmt.Sprintf("/admin/groups/%v/attributes", group.Id), http.StatusFound)
	}
}