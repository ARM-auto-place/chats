package models

import "go/types"

type Err400 struct {
	// @tg example=``
	Data types.Nil `json:"data,omitempty"`
	// @tg desc=`Флаг показывающий, что ответ пришел с ошибкой`
	// @tg example=`true`
	Error bool `json:"error"`
	// @tg desc=`Заголовок ошибки`
	// @tg example=`chat.errors.badRequest`
	ErrorText string `json:"errorText"`
	// @tg desc=`Текст ошибки, при ответе`
	// @tg example=`{"field":"chatID"}`
	AdditionalErrors map[string]string `json:"additionalErrors"`
}

type Err401 struct {
	// @tg example=``
	Data types.Nil `json:"data,omitempty"`
	// @tg desc=`Флаг показывающий, что ответ пришел с ошибкой`
	// @tg example=`true`
	Error bool `json:"error"`
	// @tg desc=`Заголовок ошибки`
	// @tg example=`chat.errors.unauthorized`
	ErrorText string `json:"errorText"`
	// @tg desc=`Текст ошибки, при ответе, со статус кодом 401, не указывается`
	AdditionalErrors types.Nil `json:"additionalErrors,omitempty"`
}

type Err403 struct {
	// @tg example=``
	Data types.Nil `json:"data,omitempty"`
	// @tg desc=`Флаг показывающий, что ответ пришел с ошибкой`
	// @tg example=`true`
	Error bool `json:"error"`
	// @tg desc=`Заголовок ошибки`
	// @tg example=`chat.errors.forbidden`
	ErrorText string `json:"errorText"`
	// @tg desc=`Текст ошибки, при ответе, со статус кодом 403, не указывается`
	AdditionalErrors types.Nil `json:"additionalErrors,omitempty"`
}

type Err500 struct {
	// @tg example=``
	Data types.Nil `json:"data,omitempty"`
	// @tg desc=`Флаг показывающий, что ответ пришел с ошибкой`
	// @tg example=`true`
	Error bool `json:"error"`
	// @tg desc=`Заголовок ошибки`
	// @tg example=`chat.errors.internalError`
	ErrorText string `json:"errorText"`
	// @tg desc=`Текст ошибки, при ответе, со статус кодом 403, не указывается`
	AdditionalErrors types.Nil `json:"additionalErrors,omitempty"`
}
