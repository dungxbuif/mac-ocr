package security

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

const maxAttachmentBytes = 100 * 1024 * 1024 // 100 MiB

var validExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".webp": true,
	".tiff": true,
	".tif":  true,
	".pdf":  true,
}

type Validator struct{}

func NewValidator() *Validator {
	return &Validator{}
}

func (v *Validator) ValidateURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("URL is empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("invalid URL format. Scheme must be http:// or https://")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("unsupported URL scheme '%s'. Only http:// and https:// are allowed", u.Scheme)
	}

	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "loopback" || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("blocked URL: private or loopback hostname '%s' is not allowed", host)
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if err := v.checkIPSecurity(ip); err != nil {
			return err
		}
	} else {
		ips, err := net.LookupIP(host)
		if err == nil {
			for _, resolvedIP := range ips {
				if err := v.checkIPSecurity(resolvedIP); err != nil {
					return fmt.Errorf("blocked URL '%s': resolves to private IP %s", host, resolvedIP.String())
				}
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(u.Path))
	if ext != "" && !validExtensions[ext] {
		return fmt.Errorf("unsupported file extension '%s'. Allowed: .png, .jpg, .jpeg, .webp, .tiff, .pdf", ext)
	}

	return nil
}

func (v *Validator) ValidateAttachment(filename string, sizeBytes int) error {
	if sizeBytes > maxAttachmentBytes {
		return fmt.Errorf("file size (%d bytes) exceeds maximum limit of 100 MiB", sizeBytes)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext != "" && !validExtensions[ext] {
		return fmt.Errorf("unsupported file extension '%s'. Allowed: .png, .jpg, .jpeg, .webp, .tiff, .pdf", ext)
	}

	return nil
}

func (v *Validator) checkIPSecurity(ip net.IP) error {
	if ip.IsLoopback() {
		return fmt.Errorf("blocked: loopback IP address %s is not allowed", ip.String())
	}
	if ip.IsPrivate() {
		return fmt.Errorf("blocked: private IP address %s is not allowed", ip.String())
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("blocked: link-local IP address %s is not allowed", ip.String())
	}
	if ip.IsMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("blocked: invalid IP address %s", ip.String())
	}

	if ip.String() == "169.254.169.254" {
		return errors.New("blocked: cloud metadata service IP address is forbidden")
	}

	return nil
}
