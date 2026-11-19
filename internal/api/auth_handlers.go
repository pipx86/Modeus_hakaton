package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"activity-descriptor/internal/auth"
	"activity-descriptor/internal/domain"

	"golang.org/x/crypto/bcrypt"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func decodeStrictJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return errors.New("некорректный JSON")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("после тела запроса есть лишние данные")
	}
	return nil
}

type registerRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authUserDTO struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
}

type authResponse struct {
	Token string      `json:"token"`
	User  authUserDTO `json:"user"`
}

func toAuthUser(user *domain.User) authUserDTO {
	return authUserDTO{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
	}
}

func validRegistration(req *registerRequest) string {
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if !emailPattern.MatchString(req.Email) {
		return "укажите корректный email"
	}
	if len(req.DisplayName) < 2 || len([]rune(req.DisplayName)) > 100 {
		return "имя должно быть от 2 до 100 символов"
	}
	if len(req.Password) < 8 {
		return "пароль должен содержать минимум 8 символов"
	}
	if len(req.Password) > 200 {
		return "пароль слишком длинный"
	}
	return ""
}

// handleRegister godoc
// @Summary      Register a user
// @Description  Creates a user with email and password. The first registered user becomes admin, all following users get the user role.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      registerRequest  true  "Registration data"
// @Success      201   {object}  authResponse
// @Failure      400   {object}  errorBody
// @Failure      409   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Router       /api/v1/auth/register [post]
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(authUserStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "регистрация требует хранилище пользователей")
		return
	}
	var req registerRequest
	if err := decodeStrictJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	if message := validRegistration(&req); message != "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", message)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать пароль")
		return
	}
	role := auth.RoleUser
	count, err := store.CountUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось проверить пользователей")
		return
	}
	if count == 0 {
		role = auth.RoleAdmin
	}
	user, err := store.CreateUser(r.Context(), req.Email, req.DisplayName, string(hash), string(role))
	if errors.Is(err, ErrEmailTaken) {
		writeErr(w, http.StatusConflict, "EMAIL_TAKEN", "пользователь с таким email уже зарегистрирован")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось создать пользователя")
		return
	}
	token, err := auth.Issue(user.ID, role, 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось выпустить токен")
		return
	}
	writeJSON(w, http.StatusCreated, authResponse{Token: token, User: toAuthUser(user)})
}

// handleLogin godoc
// @Summary      Log in with email and password
// @Description  Returns a 24-hour JWT for an existing user.
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      loginRequest  true  "Credentials"
// @Success      200   {object}  authResponse
// @Failure      400   {object}  errorBody
// @Failure      401   {object}  errorBody
// @Failure      500   {object}  errorBody
// @Router       /api/v1/auth/login [post]
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(authUserStore)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "вход требует хранилище пользователей")
		return
	}
	var req loginRequest
	if err := decodeStrictJSON(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !emailPattern.MatchString(req.Email) || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "укажите email и пароль")
		return
	}
	user, err := store.UserByEmail(r.Context(), req.Email)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "неверный email или пароль")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "UNAUTHORIZED", "неверный email или пароль")
		return
	}
	token, err := auth.Issue(user.ID, auth.Role(user.Role), 24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "INTERNAL", "не удалось выпустить токен")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, User: toAuthUser(user)})
}
