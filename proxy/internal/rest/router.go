package rest

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"macocr/proxy/admin"
	"macocr/proxy/docs"
	"macocr/proxy/internal/rest/middleware"
)

type Router struct {
	engine *gin.Engine
}

func NewRouter(
	logger *slog.Logger,
	health *HealthHandler,
	auth *AuthHandler,
	doc *DocumentHandler,
	batch *BatchHandler,
	upload *UploadHandler,
	cap *CapabilitiesHandler,
	adminAuth *AdminAuthHandler,
	webhook *WebhookHandler,
	notifications *NotificationHandler,
	mcp *MCPHandler,
) *Router {
	gin.SetMode(gin.ReleaseMode)
	gin.EnableJsonDecoderDisallowUnknownFields()
	engine := gin.New()

	engine.Use(
		middleware.RequestID(),
		middleware.Logger(logger),
		middleware.Recovery(logger),
	)

	health.Register(engine.Group("/"))
	docsHandler := docs.Handler(doc.apiBaseURL, doc.docsBaseURL)
	engine.GET("/", gin.WrapH(docsHandler))
	engine.GET("/docs/*filepath", gin.WrapH(http.StripPrefix("/docs", docsHandler)))
	engine.GET("/api/v1/docs", gin.WrapH(docs.SwaggerHandler(doc.apiBaseURL)))
	engine.GET("/api/v1/docs/", gin.WrapH(docs.SwaggerHandler(doc.apiBaseURL)))
	engine.GET("/api/v1/openapi.json", gin.WrapH(OpenAPIHandler()))

	engine.Any("/admin", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/admin/")
	})
	engine.Any("/admin/*filepath", gin.WrapH(http.StripPrefix("/admin", admin.Handler())))

	engine.POST("/webhooks/native/events", webhook.HandleNativeEvent)
	engine.POST("/webhooks/native/verify", webhook.HandleNativeVerify)
	engine.POST("/mcp", auth.RequireAPIKey(), mcp.Post)
	engine.GET("/mcp", auth.RequireAPIKey(), mcp.Get)

	api := engine.Group("/v1")

	api.GET("/ocr/capabilities", cap.Get)
	api.GET("/events", auth.RequireAPIKey(), notifications.Events)

	api.POST("/auth/login", adminAuth.Login)
	api.POST("/auth/logout", adminAuth.Logout)
	api.GET("/auth/me", adminAuth.Me)

	adminGrp := api.Group("/admin", adminAuth.RequireAdminSession())
	adminGrp.GET("/dashboard", adminAuth.DashboardStats)
	adminGrp.GET("/documents", func(c *gin.Context) {
		docList, err := doc.svc.ListDocumentsAdmin(c.Request.Context(), "", 100, 0)
		if err != nil {
			RespondError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"documents": docList})
	})

	// User self-service or Admin routes
	userSession := api.Group("/", adminAuth.RequireSession())
	userSession.GET("/users/:id/apikeys", auth.ListAPIKeys)
	userSession.POST("/users/:id/apikeys", auth.CreateAPIKey)
	userSession.PATCH("/users/:id/apikeys/:kid", auth.UpdateAPIKey)
	userSession.DELETE("/users/:id/apikeys/:kid", auth.RevokeAPIKey)
	userSession.POST("/users/:id/reset-password", auth.ResetPassword)
	userSession.GET("/users/:id/config", auth.GetAccountConfig)

	// Admin-only management routes
	accountAdmin := api.Group("/", adminAuth.RequireAdminSession())
	accountAdmin.POST("/users", auth.CreateUser)
	accountAdmin.GET("/users", auth.ListUsers)
	accountAdmin.GET("/users/:id", auth.GetUser)
	accountAdmin.PATCH("/users/:id", auth.UpdateUser)
	accountAdmin.POST("/users/:id/deactivate", auth.DeactivateUser)
	accountAdmin.POST("/users/:id/reactivate", auth.ReactivateUser)
	accountAdmin.PATCH("/users/:id/config", auth.UpdateAccountConfig)
	accountAdmin.POST("/users/:id/config/reset-quota", auth.ResetDocQuota)

	registerDocRoutes(api.Group("/documents", auth.RequireAPIKey()), doc)

	registerBatchRoutes(api.Group("/batches", auth.RequireAPIKey()), batch)
	api.POST("/uploads/presign", auth.RequireAPIKey(), upload.Presign)

	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) ||
			strings.HasPrefix(path, "/v1") ||
			strings.HasPrefix(path, "/api/v1") ||
			strings.HasPrefix(path, "/mcp") ||
			strings.HasPrefix(path, "/webhooks") ||
			strings.HasPrefix(path, "/admin") {
			c.Status(http.StatusNotFound)
			return
		}
		c.Status(http.StatusOK)
		docsHandler.ServeHTTP(c.Writer, c.Request)
	})

	return &Router{engine: engine}
}

func registerDocRoutes(g *gin.RouterGroup, h *DocumentHandler) {
	g.POST("", h.Submit)
	g.GET("/:id", h.Get)
}

func registerBatchRoutes(g *gin.RouterGroup, h *BatchHandler) {
	g.POST("", h.Submit)
}

func (r *Router) Handler() http.Handler { return r.engine }

func (r *Router) ListenAndServe(ctx context.Context, addr string, shutdownTimeout time.Duration) error {
	srv := newHTTPServer(addr, r.engine)
	return serveGracefully(ctx, srv, shutdownTimeout)
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
	}
}
