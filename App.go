package catu

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Masterminds/sprig"
	"github.com/go-catupiry/catu/acl"
	"github.com/go-catupiry/catu/configuration"
	"github.com/go-catupiry/catu/helpers"
	"github.com/go-catupiry/catu/http_client"
	"github.com/go-catupiry/catu/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gookit/event"
	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	gorm_logger "gorm.io/gorm/logger"

	"gorm.io/gorm"
)

type App struct {
	InitTime time.Time

	Events *event.Manager

	Configuration configuration.Configer
	// Default database
	DB *gorm.DB
	// avaible databases
	DBs map[string]*gorm.DB

	Plugins map[string]Pluginer

	Models map[string]interface{}

	router    *echo.Echo
	Resources map[string]*HTTPResource

	routerGroups    map[string]*echo.Group
	apiRouterGroups map[string]*echo.Group

	RolesString string
	RolesList   map[string]acl.Role

	templates         *template.Template
	templateFunctions template.FuncMap
}

func (r *App) RegisterPlugin(p Pluginer) {
	if p.GetName() == "" {
		panic("Plugin.RegisterPlugin Name should be returned from GetName method")
	}

	r.Plugins[p.GetName()] = p
}

func (r *App) GetRouter() *echo.Echo {
	return r.router
}

func (r *App) GetTemplates() *template.Template {
	return r.templates
}

func (r *App) Bootstrap() error {
	var err error

	logrus.Debug("catu.App.Bootstrap running")
	// default roles and permissions, override it on your app
	json.Unmarshal([]byte(r.RolesString), &r.RolesList)

	for _, p := range r.Plugins {
		err = p.Init(r)
		if err != nil {
			return errors.Wrap(err, "App.Bootstrap | Error on run plugin init "+p.GetName())
		}
	}

	r.Events.MustTrigger("configuration", event.M{"app": r})

	err = r.InitDatabase("default", configuration.GetEnv("DB_ENGINE", "sqlite"), true)
	if err != nil {
		return err
	}

	http_client.Init()

	r.Events.MustTrigger("bindMiddlewares", event.M{"app": r})
	r.Events.MustTrigger("bindRoutes", event.M{"app": r})
	r.Events.MustTrigger("setResponseFormats", event.M{"app": r})
	r.Events.MustTrigger("setTemplateFunctions", event.M{"app": r})

	logrus.WithFields(logrus.Fields{
		"count": len(r.templateFunctions),
	}).Debug("catu.App.Bootstrap template functions loaded")

	err = r.LoadTemplates()
	if err != nil {
		return errors.Wrap(err, "App.Bootstrap Error on LoadTemplates")
	}

	r.router.Renderer = &TemplateRenderer{
		templates: r.GetTemplates(),
	}

	r.Events.MustTrigger("bootstrap", event.M{"app": r})

	return nil
}

func (r *App) StartHTTPServer() error {
	port := r.Configuration.Get("PORT")
	if port == "" {
		port = "8080"
	}

	logrus.Info("Server listening on port " + port)
	return http.ListenAndServe(":"+port, r.GetRouter())
}

func (r *App) SetRouterGroup(name, path string) *echo.Group {
	if r.routerGroups[name] == nil {
		r.routerGroups[name] = r.router.Group(path)
	}
	return r.routerGroups[name]
}

func (r *App) GetRouterGroup(name string) *echo.Group {
	return r.routerGroups[name]
}

func (r *App) SetAPIRouterGroup(name, path string) *echo.Group {
	if r.apiRouterGroups[name] == nil {
		r.apiRouterGroups[name] = r.routerGroups["api"].Group(path)
	}
	return r.apiRouterGroups[name]
}

func (r *App) GetAPIRouterGroup(name string) *echo.Group {
	return r.apiRouterGroups[name]
}

// Set Resource CRUD.
// Now we only supports HTTP Resources / Ex Rest
func (r *App) SetResource(name string, httpController HTTPController, routerGroup *echo.Group) error {
	routerGroup.GET("", httpController.Query)
	routerGroup.GET("/count", httpController.Count)
	routerGroup.POST("", httpController.Create)
	routerGroup.GET("/:id", httpController.FindOne)
	routerGroup.POST("/:id", httpController.Update)
	routerGroup.PATCH("/:id", httpController.Update)
	routerGroup.PUT("/:id", httpController.Update)
	routerGroup.DELETE("/:id", httpController.Delete)

	r.Resources[name] = &HTTPResource{
		Name:       name,
		Controller: &httpController,
	}

	return nil
}

func (r *App) InitDatabase(name, engine string, isDefault bool) error {
	var err error
	var db *gorm.DB

	dbURI := r.Configuration.GetF("DB_URI", "test.sqlite?charset=utf8mb4")
	dbSlowThreshold := r.Configuration.GetInt64F("DB_SLOW_THRESHOLD", 400)
	logQuery := r.Configuration.GetF("LOG_QUERY", "")

	logrus.WithFields(logrus.Fields{
		"dbURI":           dbURI,
		"dbSlowThreshold": dbSlowThreshold,
		"logQuery":        logQuery,
	}).Debug("catu.App.InitDatabase starting db with configs")

	if dbURI == "" {
		return errors.New("catu.App.InitDatabase DB_URI environment variable is required")
	}

	dsn := dbURI + "?charset=utf8mb4&parseTime=True&loc=Local"

	dbLogger := gorm_logger.New(log.New(os.Stdout, "\r\n", log.LstdFlags), gorm_logger.Config{
		SlowThreshold:             time.Duration(dbSlowThreshold) * time.Millisecond,
		LogLevel:                  gorm_logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  true,
	})

	logg := dbLogger.LogMode(gorm_logger.Warn)

	if logQuery != "" {
		logg = dbLogger.LogMode(gorm_logger.Info)
	}

	switch engine {
	case "mysql":
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
			Logger: logg,
		})
	case "sqlite":
		db, err = gorm.Open(sqlite.Open(dbURI), &gorm.Config{
			Logger: logg,
		})

	default:
		return errors.New("catu.App.InitDatabase invalid database engine. Options available: mysql or sqlite")
	}

	if err != nil {
		return errors.Wrap(err, "catu.App.InitDatabase error on database connection")
	}

	if isDefault {
		r.DB = db
	}

	return nil
}

func (r *App) SetModel(name string, f interface{}) {
	r.Models[name] = f
}

func (r *App) GetModel(name string) interface{} {
	return r.Models[name]
}

func (r *App) SetTemplateFunction(name string, f interface{}) {
	r.templateFunctions[name] = f
}

func (r *App) Can(permission string, userRoles []string) bool {
	// first check if user is administrator
	for i := range userRoles {
		if userRoles[i] == "administrator" {
			return true
		}
	}

	for j := range userRoles {
		R := r.RolesList[userRoles[j]]
		if R.Can(permission) {
			return true
		}
	}

	return false
}

func (r *App) LoadTemplates() error {
	rootDir := r.Configuration.GetF("TEMPLATE_FOLDER", "./templates")
	disableTemplating := r.Configuration.GetBool("TEMPLATE_DISABLE")

	if disableTemplating {
		return nil
	}

	tpls, err := findAndParseTemplates(rootDir, r.templateFunctions)
	if err != nil {
		logrus.WithFields(logrus.Fields{
			// "error":   errHealthCheckHandlerr,
			"rootDir": rootDir,
		}).Error("catu.App.LoadTemplates Error on parse templates")
		r.templates = tpls
		return err
	}

	r.templates = tpls

	logrus.WithFields(logrus.Fields{
		"count": len(r.templates.Templates()),
	}).Debug("catu.App.ParseTemplates templates loaded")

	return nil
}

// Run migrations
func (r *App) Migrate() error {
	err, _ := r.Events.Fire("migrate", event.M{"app": r})
	if err != nil {
		return errors.Wrap(err, "App.Migrate migrate error")
	}

	return nil
}

func newApp() *App {
	var app App

	app.Events = event.NewManager("app")
	app.RolesString, _ = acl.LoadRoles()

	logger.Init()
	app.Configuration = configuration.NewCfg()
	app.routerGroups = make(map[string]*echo.Group)
	app.apiRouterGroups = make(map[string]*echo.Group)

	app.Resources = make(map[string]*HTTPResource)
	app.router = echo.New()

	app.router.Binder = &CustomBinder{}
	app.router.HTTPErrorHandler = CustomHTTPErrorHandler
	app.router.Validator = &helpers.CustomValidator{Validator: validator.New()}

	app.router.GET("/health", HealthCheckHandler)
	app.Plugins = make(map[string]Pluginer)

	app.templates = &template.Template{}

	app.SetRouterGroup("main", "/")
	app.SetRouterGroup("public", "/public")

	apiRouterGroup := app.SetRouterGroup("api", "/api")
	apiRouterGroup.GET("", HealthCheckHandler)

	app.templateFunctions = sprig.FuncMap()

	return &app
}
