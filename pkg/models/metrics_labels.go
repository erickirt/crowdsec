// Code generated by go-swagger; DO NOT EDIT.

package models

// This file was generated by the swagger tool.
// Editing this file might prove futile when you re-run the swagger generate command

import (
	"context"

	"github.com/go-openapi/errors"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
)

// MetricsLabels MetricsLabels
//
// swagger:model MetricsLabels
type MetricsLabels map[string]string

// Validate validates this metrics labels
func (m MetricsLabels) Validate(formats strfmt.Registry) error {
	var res []error

	for k := range m {

		if err := validate.MaxLength(k, "body", m[k], 255); err != nil {
			return err
		}

	}

	if len(res) > 0 {
		return errors.CompositeValidationError(res...)
	}
	return nil
}

// ContextValidate validates this metrics labels based on context it is used
func (m MetricsLabels) ContextValidate(ctx context.Context, formats strfmt.Registry) error {
	return nil
}
