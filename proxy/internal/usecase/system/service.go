package system

import (
	"context"
	"errors"
)

type ReadyChecker interface {
	Ready(ctx context.Context) error
}

type Service struct {
	checkers []ReadyChecker
}

func NewService(checkers ...ReadyChecker) *Service {
	return &Service{checkers: checkers}
}

func (s *Service) Ready(ctx context.Context) error {
	var errs []error
	for _, c := range s.checkers {
		if err := c.Ready(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
