package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/modules/imagegen"
	"github.com/QuantumNous/new-api/oauth"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	_ "github.com/QuantumNous/new-api/setting/performance_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	_ "net/http/pprof"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "plugin" {
		os.Exit(jsplugin.RunCLI(os.Args[2:], os.Stdout, os.Stderr))
	}
	if err := runApplication(); err != nil {
		common.FatalLog(common.MaskSensitiveLogInfo(err.Error()))
	}
}

func runApplication() error {
	startTime := time.Now()
	kitutil.SetLogging(common.SysLog, func(message string) {
		logger.LogError(nil, message)
	})
	kitutil.SetSystemErrorLogging(common.SysError)

	safeToCloseResources := true
	defer func() {
		if !safeToCloseResources {
			return
		}
		if common.RDB != nil {
			if err := common.RDB.Close(); err != nil {
				common.SysError("failed to close Redis: " + err.Error())
			}
		}
		if err := model.CloseDB(); err != nil {
			common.SysError("failed to close database: " + err.Error())
		}
	}()

	app := newApplicationRuntime()
	err := InitResources(app.workers)
	if err != nil {
		return fmt.Errorf("failed to initialize resources: %w", err)
	}

	common.SysLog(common.LogText("New API %s started", common.Version))
	if os.Getenv("GIN_MODE") != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}
	if common.DebugEnabled {
		common.SysLog(common.LogText("running in debug mode"))
	}

	kitutil.Debug.Store(common.DebugEnabled)

	if common.RedisEnabled {
		// for compatibility with old versions
		common.MemoryCacheEnabled = true
	}
	if common.MemoryCacheEnabled {
		common.SysLog(common.LogText("memory cache enabled"))
		common.SysLog(common.LogText("sync frequency: %d seconds", common.SyncFrequency))

		if err := model.RefreshChannelCache(context.Background()); err != nil {
			return fmt.Errorf("initial channel cache load failed: %w", err)
		}
	}

	// Warm pricing after channel cache initialization so Advanced Custom
	// endpoint inference can read cached route settings on first request.
	model.GetPricing()

	var balanceInterval time.Duration
	if value := os.Getenv("CHANNEL_UPDATE_FREQUENCY"); value != "" {
		frequency, err := strconv.Atoi(value)
		if err != nil || frequency <= 0 || frequency > 525600 {
			return errors.New("CHANNEL_UPDATE_FREQUENCY must be positive minutes, at most one year")
		}
		balanceInterval = time.Duration(frequency) * time.Minute
	}

	// Wire task polling adaptor factory (breaks service -> relay import cycle).
	// Must run before the system task runner starts: the async_task_poll handler
	// calls service.RunTaskPollingOnce, which needs this factory set.
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		a := relay.GetTaskAdaptor(platform)
		if a == nil {
			return nil
		}
		return a
	}

	// Register the periodic channel test, upstream model update, and async task
	// polling (Midjourney / Suno / video) jobs as scheduled system tasks
	// (DB-lease dedup across masters + run history), then start the runner that
	// schedules and executes them. Master-only execution and the UpdateTask
	// switch are enforced inside the runner and each handler's Enabled().
	if err := extension.Default().ApplyLifecycle(extension.PhaseBeforeBackgroundWorkers); err != nil {
		return fmt.Errorf("failed to apply extension startup lifecycle: %w", err)
	}
	controller.RegisterScheduledSystemTasks()

	if os.Getenv("BATCH_UPDATE_ENABLED") == "true" {
		common.BatchUpdateEnabled = true
		common.SysLog("batch update enabled with interval " + strconv.Itoa(common.BatchUpdateInterval) + "s")
	}

	// Initialize HTTP server
	server := gin.New()
	if err := middleware.ConfigureTrustedProxies(server); err != nil {
		return fmt.Errorf("failed to configure trusted proxies: %w", err)
	}
	server.Use(gin.CustomRecovery(func(c *gin.Context, err any) {
		common.SysLog(common.LogText("panic detected: %v", err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"message": fmt.Sprintf("Panic detected, error: %v. Please submit a issue here: https://github.com/Calcium-Ion/new-api", err),
				"type":    "new_api_panic",
			},
		})
	}))
	// This will cause SSE not to work!!!
	//server.Use(gzip.Gzip(gzip.DefaultCompression))
	server.Use(middleware.RequestId())
	server.Use(middleware.Version())
	server.Use(middleware.I18n())
	middleware.SetUpLogger(server)
	InjectUmamiAnalytics()
	InjectGoogleAnalytics()

	// 设置路由
	if err := router.SetRouter(server, router.WebAssets{
		BuildFS:   buildFS,
		IndexPage: indexPage,
		Disabled:  frontendAssetsDisabled,
	}); err != nil {
		return fmt.Errorf("failed to initialize routes: %w", err)
	}
	var port = os.Getenv("PORT")
	if port == "" {
		port = strconv.Itoa(*common.Port)
	}

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           app.gate.Wrap(server),
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("failed to bind HTTP server: %w", err)
	}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	safeToCloseResources = false
	app.Start(balanceInterval, os.Getenv("ENABLE_PPROF") == "true")
	if err := common.StartPyroScope(); err != nil {
		common.SysError("start pyroscope error: " + common.MaskSensitiveLogInfo(err.Error()))
	}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- srv.Serve(listener) }()
	common.LogStartupSuccess(startTime, port)

	var runErr error
	select {
	case sig := <-quit:
		common.SysLog(fmt.Sprintf("received signal: %v, shutting down...", sig))
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("HTTP server stopped: %w", err)
			common.SysError(runErr.Error())
		}
	case err := <-app.workers.Failures():
		runErr = err
		common.SysError(err.Error())
	}
	safeToCloseResources = app.Shutdown(srv)
	if !safeToCloseResources {
		return errors.New("runtime shutdown or finalization did not complete")
	}

	// A worker may fail after SIGTERM selected the shutdown path. Shutdown
	// proved the group joined, so this read cannot block and preserves that
	// late failure instead of returning a successful process exit.
	if runErr == nil {
		runErr = app.workers.Wait(context.Background())
	}
	common.SysLog("server exited")
	return runErr
}

func InjectUmamiAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("UMAMI_WEBSITE_ID") != "" {
		umamiSiteID := os.Getenv("UMAMI_WEBSITE_ID")
		umamiScriptURL := os.Getenv("UMAMI_SCRIPT_URL")
		if umamiScriptURL == "" {
			umamiScriptURL = "https://analytics.umami.is/script.js"
		}
		analyticsInjectBuilder.WriteString("<script defer src=\"")
		analyticsInjectBuilder.WriteString(umamiScriptURL)
		analyticsInjectBuilder.WriteString("\" data-website-id=\"")
		analyticsInjectBuilder.WriteString(umamiSiteID)
		analyticsInjectBuilder.WriteString("\"></script>")
	}
	analyticsInjectBuilder.WriteString("<!--Umami QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--umami-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InjectGoogleAnalytics() {
	analyticsInjectBuilder := &strings.Builder{}
	if os.Getenv("GOOGLE_ANALYTICS_ID") != "" {
		gaID := os.Getenv("GOOGLE_ANALYTICS_ID")
		// Google Analytics 4 (gtag.js)
		analyticsInjectBuilder.WriteString("<script async src=\"https://www.googletagmanager.com/gtag/js?id=")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("\"></script>")
		analyticsInjectBuilder.WriteString("<script>")
		analyticsInjectBuilder.WriteString("window.dataLayer = window.dataLayer || [];")
		analyticsInjectBuilder.WriteString("function gtag(){dataLayer.push(arguments);}")
		analyticsInjectBuilder.WriteString("gtag('js', new Date());")
		analyticsInjectBuilder.WriteString("gtag('config', '")
		analyticsInjectBuilder.WriteString(gaID)
		analyticsInjectBuilder.WriteString("');")
		analyticsInjectBuilder.WriteString("</script>")
	}
	analyticsInjectBuilder.WriteString("<!--Google Analytics QuantumNous-->\n")
	analyticsInject := []byte(analyticsInjectBuilder.String())
	placeholder := []byte("<!--Google Analytics-->\n")
	indexPage = bytes.ReplaceAll(indexPage, placeholder, analyticsInject)
}

func InitResources(workers *lifecycle.Group) error {
	// Initialize resources here if needed
	// This is a placeholder function for future resource initialization
	err := godotenv.Load(".env")
	if err != nil {
		if common.DebugEnabled {
			common.SysLog(common.LogText("No .env file found, using default environment variables. If needed, please create a .env file and set the relevant variables."))
		}
	}

	// 加载环境变量
	common.InitEnv()

	logger.SetupLogger()

	// Initialize model settings
	ratio_setting.InitRatioSettings()

	service.InitHttpClient()

	if err := chuanapi.RegisterRuntimeServices(); err != nil {
		common.FatalLog("failed to register chuanapi runtime services: " + err.Error())
		return err
	}
	// Runtime funding implementations are composed explicitly and frozen before
	// any request can create a BillingSession.
	fundingFactories := moneyport.NewFactoryRegistry()
	if err := service.RegisterHostFundingFactories(fundingFactories); err != nil {
		common.FatalLog("failed to register host funding factories: " + err.Error())
		return err
	}
	if err := chuanapi.RegisterFundingFactories(fundingFactories); err != nil {
		common.FatalLog("failed to register chuanapi funding factories: " + err.Error())
		return err
	}
	if err := service.ConfigureFundingFactoryRegistry(fundingFactories); err != nil {
		common.FatalLog("failed to configure funding factory registry: " + err.Error())
		return err
	}

	// Compose the extension registry before any migration runs: model.InitDB
	// reads extension.Default() to append extension models to the core
	// AutoMigrate call and to run extension data steps. Registration is
	// build-time and explicit, so a wiring failure here must abort startup
	// rather than silently start with an unmigrated extension schema.
	registry := extension.NewRegistry()
	if err := imagegen.Register(registry, workers); err != nil {
		common.FatalLog("failed to register image generation module: " + err.Error())
		return err
	}
	if err := chuanapi.Register(registry); err != nil {
		common.FatalLog("failed to register chuanapi extension: " + err.Error())
		return err
	}
	if err := extension.SetDefault(registry); err != nil {
		common.FatalLog("failed to install extension registry: " + err.Error())
		return err
	}

	// Initialize SQL Database
	err = model.InitDB()
	if err != nil {
		common.FatalLog(common.LogText("failed to initialize database: %s", err.Error()))
		return err
	}
	if err = authz.Init(model.DB); err != nil {
		common.FatalLog(common.LogText("failed to initialize authorization: %s", err.Error()))
		return err
	}
	if common.PasswordLoginEncryptionEnabled {
		if err = model.InitPasswordEncryption(); err != nil {
			common.FatalLog(common.LogText("failed to initialize password encryption: %s", err.Error()))
			return err
		}
	}

	model.CheckSetup()

	// Initialize options, should after model.InitDB()
	if common.IsMasterNode {
		if err := model.MigrateRetiredFrontendOptions(); err != nil {
			common.SysError(common.LogText("failed to migrate retired frontend options: %s", err.Error()))
		}
	}
	model.InitOptionMap()

	// 清理旧的磁盘缓存文件
	common.CleanupOldCacheFiles()

	// Initialize SQL Database
	err = model.InitLogDB()
	if err != nil {
		return err
	}

	// Initialize Redis
	err = common.InitRedisClient()
	if err != nil {
		return err
	}

	// Initialize i18n
	err = i18n.Init()
	if err != nil {
		common.SysError(common.LogText("failed to initialize i18n: %s", err.Error()))
		// Don't return error, i18n is not critical
	} else {
		common.SysLog(common.LogText("i18n initialized with languages: %s", strings.Join(i18n.SupportedLanguages(), ", ")))
	}
	// Register user language loader for lazy loading
	i18n.SetUserLangLoader(model.GetUserLanguage)

	// Load custom OAuth providers from database
	err = oauth.LoadCustomProviders()
	if err != nil {
		common.SysError(common.LogText("failed to load custom OAuth providers: %s", err.Error()))
		// Don't return error, custom OAuth is not critical
	}

	return nil
}
