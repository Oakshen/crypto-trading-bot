package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
)

// SessionManager manages user sessions
// SessionManager 管理用户会话
type SessionManager struct {
	sessions map[string]*Session // sessionID -> Session
	mu       sync.RWMutex
}

// Session represents a user session
// Session 表示一个用户会话
type Session struct {
	ID        string
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// NewSessionManager creates a new session manager
// NewSessionManager 创建新的会话管理器
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*Session),
	}

	// Start cleanup goroutine to remove expired sessions
	// 启动清理协程以移除过期会话
	go sm.cleanupExpiredSessions()

	return sm
}

// CreateSession creates a new session for a user
// CreateSession 为用户创建新会话
func (sm *SessionManager) CreateSession(username string) (*Session, error) {
	sessionID, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	session := &Session{
		ID:        sessionID,
		Username:  username,
		CreatedAt: time.Now(),
		ExpiresAt: time.Now().Add(24 * time.Hour), // 24 hours expiration / 24小时过期
	}

	sm.mu.Lock()
	sm.sessions[sessionID] = session
	sm.mu.Unlock()

	return session, nil
}

// GetSession retrieves a session by ID
// GetSession 根据 ID 获取会话
func (sm *SessionManager) GetSession(sessionID string) (*Session, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, exists := sm.sessions[sessionID]
	if !exists {
		return nil, false
	}

	// Check if session has expired
	// 检查会话是否已过期
	if time.Now().After(session.ExpiresAt) {
		return nil, false
	}

	return session, true
}

// DeleteSession removes a session
// DeleteSession 移除会话
func (sm *SessionManager) DeleteSession(sessionID string) {
	sm.mu.Lock()
	delete(sm.sessions, sessionID)
	sm.mu.Unlock()
}

// cleanupExpiredSessions periodically removes expired sessions
// cleanupExpiredSessions 定期移除过期会话
func (sm *SessionManager) cleanupExpiredSessions() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for range ticker.C {
		sm.mu.Lock()
		now := time.Now()
		for id, session := range sm.sessions {
			if now.After(session.ExpiresAt) {
				delete(sm.sessions, id)
			}
		}
		sm.mu.Unlock()
	}
}

// generateSessionID generates a random session ID
// generateSessionID 生成随机会话 ID
func generateSessionID() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// AuthMiddleware returns a middleware that checks if user is authenticated
// AuthMiddleware 返回检查用户是否已认证的中间件
func (s *Server) AuthMiddleware() app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		// Get session cookie
		// 获取会话 cookie
		sessionID := string(c.Cookie("session_id"))

		if sessionID == "" {
			// No session cookie, redirect to login
			// 没有会话 cookie，重定向到登录页
			c.Redirect(http.StatusFound, []byte("/login"))
			c.Abort()
			return
		}

		// Check if session exists and is valid
		// 检查会话是否存在且有效
		session, exists := s.sessionManager.GetSession(sessionID)
		if !exists {
			// Invalid session, redirect to login
			// 无效会话，重定向到登录页
			c.Redirect(http.StatusFound, []byte("/login"))
			c.Abort()
			return
		}

		// Session is valid, store username in context for later use
		// 会话有效，将用户名存储在上下文中供后续使用
		c.Set("username", session.Username)
		c.Next(ctx)
	}
}

// handleLogin displays the login page or processes login form
// handleLogin 显示登录页面或处理登录表单
func (s *Server) handleLogin(ctx context.Context, c *app.RequestContext) {
	// If already logged in, redirect to home
	// 如果已登录，重定向到首页
	sessionID := string(c.Cookie("session_id"))
	if sessionID != "" {
		if _, exists := s.sessionManager.GetSession(sessionID); exists {
			c.Redirect(http.StatusFound, []byte("/"))
			return
		}
	}

	// Check if this is a POST request (login form submission)
	// 检查是否为 POST 请求（登录表单提交）
	if string(c.Method()) == "POST" {
		// Get form values
		// 获取表单值
		username := c.PostForm("username")
		password := c.PostForm("password")

		// Validate credentials
		// 验证凭据
		if username == s.config.WebUsername && password == s.config.WebPassword {
			// Create session
			// 创建会话
			session, err := s.sessionManager.CreateSession(username)
			if err != nil {
				s.logger.Error("创建会话失败: " + err.Error())
				c.JSON(http.StatusInternalServerError, utils.H{"error": "创建会话失败"})
				return
			}

			// Set session cookie
			// 设置会话 cookie
			c.SetCookie(
				"session_id",
				session.ID,
				int(24*time.Hour.Seconds()), // 24 hours / 24小时
				"/",
				"",
				0,     // SameSite (0 = default)
				false, // Not HTTPS only (change to true in production with HTTPS) / 非仅 HTTPS
				true,  // HttpOnly
			)

			s.logger.Info("用户登录成功: " + username)

			// Redirect to home page
			// 重定向到首页
			c.Redirect(http.StatusFound, []byte("/"))
			return
		} else {
			// Invalid credentials, show login page with error
			// 无效凭据，显示登录页面并带错误提示
			s.renderLoginPage(c, "用户名或密码错误")
			return
		}
	}

	// GET request, show login page
	// GET 请求，显示登录页面
	s.renderLoginPage(c, "")
}

// handleLogout logs out the user
// handleLogout 登出用户
func (s *Server) handleLogout(ctx context.Context, c *app.RequestContext) {
	// Get session cookie
	// 获取会话 cookie
	sessionID := string(c.Cookie("session_id"))

	if sessionID != "" {
		// Delete session
		// 删除会话
		s.sessionManager.DeleteSession(sessionID)

		// Clear cookie
		// 清除 cookie
		c.SetCookie(
			"session_id",
			"",
			-1, // Expire immediately / 立即过期
			"/",
			"",
			0, // SameSite (0 = default)
			false,
			true,
		)
	}

	s.logger.Info("用户已登出")

	// Redirect to login page
	// 重定向到登录页
	c.Redirect(http.StatusFound, []byte("/login"))
}

// renderLoginPage renders the login page with optional error message
// renderLoginPage 渲染登录页面并可选显示错误消息
func (s *Server) renderLoginPage(c *app.RequestContext, errorMsg string) {
	// We'll use a simple HTML login page for now
	// 暂时使用简单的 HTML 登录页面
	// Later we'll create a proper template
	// 稍后我们会创建正式的模板
	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>登录 - Crypto-Trading-Bot</title>
    <link rel="icon" href="data:image/svg+xml,<svg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 100 100%22><text y=%22.9em%22 font-size=%2290%22>🤖</text></svg>">
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=Archivo:wght@400;500;600;700&display=swap" rel="stylesheet">
    <link rel="stylesheet" href="/static/css/app.css">
</head>
<body class="login">
    <div class="login-card">
        <h1>Crypto-Trading-Bot</h1>
        <p class="sub">登录后查看机器人的持仓与决策</p>
        ` + func() string {
		if errorMsg != "" {
			return `<div class="notice">` + errorMsg + `</div>`
		}
		return ""
	}() + `
        <form method="POST" action="/login">
            <div class="field">
                <label for="username">用户名</label>
                <input type="text" id="username" name="username" required autofocus autocomplete="username">
            </div>
            <div class="field">
                <label for="password">密码</label>
                <input type="password" id="password" name="password" required autocomplete="current-password">
            </div>
            <button type="submit" class="btn btn-primary">登录</button>
        </form>
        <p class="login-foot">这个面板可以查看实盘持仓。请通过 HTTPS 访问，并使用强密码。</p>
    </div>
</body>
</html>`

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}
