package rest

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type CapabilitiesHandler struct{}

func NewCapabilitiesHandler() *CapabilitiesHandler {
	return &CapabilitiesHandler{}
}

func (h *CapabilitiesHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"engine":            "OCR",
		"capabilityVersion": "ocr-v1.0",
		"defaultProfile": gin.H{
			"recognitionLevel":             "accurate",
			"languages":                    []string{"vi-VN", "en-US"},
			"automaticallyDetectsLanguage": true,
			"usesLanguageCorrection":       true,
		},
		"supportedLevels":    []string{"accurate", "fast"},
		"supportedRevisions": []int{1, 2, 3},
		"supportedLanguages": []string{
			"en-US", "vi-VN", "zh-Hans", "zh-Hant", "ja-JP", "ko-KR",
			"fr-FR", "de-DE", "es-ES", "pt-BR", "it-IT", "ru-RU", "th-TH",
		},
		"limits": gin.H{
			"maxBase64Bytes":       26214400,
			"maxBatchItems":        100,
			"maxLanguagesPerDoc":   10,
			"maxCustomWords":       100,
		},
	})
}
