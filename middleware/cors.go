package middleware

import (
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

var defaultConsoleCORSAllowOrigins = []string{"http://localhost:3000", "http://127.0.0.1:3000"}

func CORS() gin.HandlerFunc {
	trustedOrigins := consoleCORSAllowOrigins()
	trustedCors := cors.New(cors.Config{
		AllowOrigins: trustedOrigins,
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin", "Accept", "Authorization", "Content-Type", "Cache-Control", "New-Api-User", "X-Auth-Session", "X-Security-Proof", "Idempotency-Key",
		},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
	publicCors := cors.New(cors.Config{
		AllowAllOrigins: true,
		AllowMethods:    []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:    []string{"*"},
		MaxAge:          12 * time.Hour,
	})
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		for _, trusted := range trustedOrigins {
			if origin != "" && origin == trusted {
				trustedCors(c)
				return
			}
		}
		publicCors(c)
	}
}

func consoleCORSAllowOrigins() []string {
	origins := make([]string, 0)
	for _, origin := range strings.Split(os.Getenv("CONSOLE_CORS_ALLOW_ORIGINS"), ",") {
		if origin = strings.TrimSpace(origin); origin != "" {
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return defaultConsoleCORSAllowOrigins
	}
	return origins
}

func Version() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-New-Api-Version", common.Version)
		c.Next()
	}
}
