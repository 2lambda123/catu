package catu

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/go-playground/validator/v10"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type ValidationResponse struct {
	Errors []*ValidationFieldError `json:"errors"`
}

type ValidationFieldError struct {
	Field   string `json:"field"`
	Tag     string `json:"tag"`
	Value   string `json:"value"`
	Message string `json:"message"`
}

func CustomHTTPErrorHandler(err error, c echo.Context) {
	code := 0
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
	}

	if ve, ok := err.(validator.ValidationErrors); ok {
		validationError(ve, err, c)
		return
	}

	if code == 0 && err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		code = 404
	}

	if code == 0 {
		code = 500
	}

	switch code {
	case 401:
		forbiddenErrorHandler(err, c)
	case 404:
		notFoundErrorHandler(err, c)
	case 500:
		internalServerErrorHandler(err, c)
	default:
		log.Println("customHTTPErrorHandler Echo error handler", err)
		errorPage := fmt.Sprintf("site/%d.html", code)
		logrus.WithFields(logrus.Fields{
			"errorPage":  errorPage,
			"statusCode": code,
			"error":      fmt.Sprintf("%+v\n", err),
		}).Warn("customHTTPErrorHandler unknow error status code")

		if err := c.File(errorPage); err != nil {
			c.Logger().Error(err)
		}
		c.Logger().Error(err)
	}
}

func forbiddenErrorHandler(err error, c echo.Context) error {
	ctx := c.Get("app").(*AppContext)

	switch ctx.ResponseContentType {
	case "application/json":
		c.JSON(http.StatusUnauthorized, err)
		return nil
	case "application/vnd.api+json":
		c.JSON(http.StatusUnauthorized, make(map[string]string))
		return nil
	default:
		ctx.Title = "Acesso restrito"

		if err := c.Render(http.StatusNotFound, "site/401", &TemplateCTX{
			Ctx: ctx,
		}); err != nil {
			c.Logger().Error(err)
		}

		return nil
	}

}

func notFoundErrorHandler(err error, c echo.Context) error {
	ctx := c.Get("app").(*AppContext)

	switch ctx.ResponseContentType {
	case "application/vnd.api+json":
		c.JSON(http.StatusNotFound, make(map[string]string))
		return nil
	case "text/html":
		ctx.Title = "Não encontrado"

		if err := c.Render(http.StatusNotFound, "site/404", &TemplateCTX{
			Ctx: ctx,
		}); err != nil {
			c.Logger().Error(err)
		}
		return nil
	default:
		c.JSON(http.StatusNotFound, make(map[string]string))
		return nil
	}
}

func validationError(ve validator.ValidationErrors, err error, c echo.Context) error {
	ctx := c.Get("app").(*AppContext)

	resp := ValidationResponse{}

	if err != nil {
		for _, err := range err.(validator.ValidationErrors) {
			var el ValidationFieldError
			el.Field = err.Field()
			el.Tag = err.Tag()
			el.Value = err.Param()
			el.Message = err.Error()
			resp.Errors = append(resp.Errors, &el)
		}
	}

	switch ctx.ResponseContentType {
	case "application/json":
		return c.JSON(http.StatusBadRequest, resp)
	case "application/vnd.api+json":
		return c.JSON(http.StatusBadRequest, resp)
	default:
		ctx.Title = "Bad request"

		if err := c.Render(http.StatusInternalServerError, "site/400", &TemplateCTX{
			Ctx: ctx,
		}); err != nil {
			c.Logger().Error(err)
		}

		return nil
	}
}

func internalServerErrorHandler(err error, c echo.Context) error {
	ctx := c.Get("app").(*AppContext)

	code := http.StatusInternalServerError
	if he, ok := err.(*echo.HTTPError); ok {
		code = he.Code
	}

	logrus.WithFields(logrus.Fields{
		"err":  fmt.Sprintf("%+v\n", err),
		"code": code,
	}).Warn("internalServerErrorHandler error")

	switch ctx.ResponseContentType {
	case "application/json":
		if he, ok := err.(*echo.HTTPError); ok {
			return c.JSON(http.StatusInternalServerError, he)
		}

		c.JSON(http.StatusInternalServerError, make(map[string]string))
		return nil
	case "application/vnd.api+json":
		c.JSON(http.StatusInternalServerError, make(map[string]string))
		return nil
	default:
		ctx.Title = "Internal server error"

		if err := c.Render(http.StatusInternalServerError, "site/500", &TemplateCTX{
			Ctx: ctx,
		}); err != nil {
			c.Logger().Error(err)
		}

		return nil
	}

}
