package custom_handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"

	customErrors "github.com/ARM-auto-place/chats/internal/errors"
)

const errInternal = customErrors.ErrInternal

type RestResponse struct {
	Data             interface{}            `json:"data"`
	Error            bool                   `json:"error"`
	ErrorText        string                 `json:"errorText"`
	AdditionalErrors map[string]interface{} `json:"additionalErrors"`
}

func sendResponse(ctx *fiber.Ctx, log zerolog.Logger, data interface{}, respError error) {
	ctx.Response().Header.Set("Content-Type", "application/json")
	ctx.Status(http.StatusOK)

	response := &RestResponse{
		Data:             data,
		Error:            respError != nil,
		AdditionalErrors: make(map[string]interface{}),
	}

	if response.Error {
		response.AdditionalErrors = nil
		ctx.Response().SetStatusCode(http.StatusInternalServerError)
		response.ErrorText = errInternal

		var customErr *customErrors.Error
		if errors.As(respError, &customErr) {
			statusCode := customErr.GetStatusCode()
			if statusCode != 0 {
				ctx.Response().SetStatusCode(statusCode)
			}
			if statusCode < http.StatusInternalServerError {
				response.ErrorText = customErr.GetTranslationKey()
				response.AdditionalErrors = customErr.Cause
			}
			if outerErr := customErr.GetOuterError(); outerErr != nil {
				log.Error().Err(outerErr).Msg("chat request failed")
			}
		}
	}

	respBody, err := json.Marshal(response)
	if err != nil {
		log.Error().Err(err).Msg("failed to marshal response")
		return
	}
	if _, err = ctx.Write(respBody); err != nil {
		log.Error().Err(err).Msg("failed to send response")
		return
	}
}
