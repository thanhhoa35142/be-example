package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/thanhhoa35142/be-example/internal/modules/adminauth"
	"github.com/thanhhoa35142/be-example/internal/platform/httpx"
)

// Admin user management. These routes sit in the authed admin group, so they
// are session-authenticated, CSRF-checked and recorded by AdminAudit like
// every other admin mutation.

// GET /v1/admin/users
func (h *AdminAuthHandler) ListUsers(c *gin.Context) {
	users, err := h.svc.ListUsers(c.Request.Context())
	if err != nil {
		httpx.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": users}})
}

type createUserRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// POST /v1/admin/users {email, password}
func (h *AdminAuthHandler) CreateUser(c *gin.Context) {
	var req createUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "expected email + password")
		return
	}
	user, err := h.svc.CreateUser(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		writeUserError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": user})
}

type adminUserActionRequest struct {
	Action   string `json:"action" binding:"required"`
	Password string `json:"password"`
}

// PATCH /v1/admin/users/:email — {"action":"set_password","password":...} |
// {"action":"reset_2fa"} | {"action":"disable"} | {"action":"enable"}
func (h *AdminAuthHandler) UpdateUser(c *gin.Context) {
	email := c.Param("email")
	var req adminUserActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body",
			"expected {\"action\":\"set_password|reset_2fa|disable|enable\"}")
		return
	}
	ctx := c.Request.Context()
	var err error
	switch req.Action {
	case "set_password":
		if req.Password == "" {
			httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid body", "set_password needs password")
			return
		}
		err = h.svc.SetPassword(ctx, email, req.Password)
	case "reset_2fa":
		err = h.svc.Reset2FA(ctx, email)
	case "disable":
		err = h.svc.SetDisabled(ctx, email, true)
	case "enable":
		err = h.svc.SetDisabled(ctx, email, false)
	default:
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid action",
			"expected set_password, reset_2fa, disable or enable")
		return
	}
	if err != nil {
		writeUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "ok"}})
}

func writeUserError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, adminauth.ErrUserNotFound):
		httpx.WriteProblem(c, http.StatusNotFound, httpx.CodeNotFound, "Admin user not found", "")
	case errors.Is(err, adminauth.ErrUserExists):
		httpx.WriteProblem(c, http.StatusConflict, httpx.CodeConflict, "Admin user exists", "an admin user with this email already exists")
	case errors.Is(err, adminauth.ErrWeakPassword):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeWeakPassword, "Weak password",
			"password must be at least "+strconv.Itoa(adminauth.MinPasswordLength)+" characters")
	case errors.Is(err, adminauth.ErrInvalidCredentials):
		httpx.WriteProblem(c, http.StatusBadRequest, httpx.CodeInvalidBody, "Invalid email", "expected a valid email address")
	default:
		httpx.WriteError(c, err)
	}
}
