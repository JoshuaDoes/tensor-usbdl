package main

import (
	"strings"
)

type Device struct {
	Brand   string
	Model   string
	Variant string
}

func (dev *Device) Set(brand, model, variant string) *Device {
	dev.Brand = brand
	dev.Model = model
	dev.Variant = variant
	return dev
}

func (dev *Device) String() (s string) {
	a := make([]string, 0)
	if dev.Brand != "" {
		a = append(a, dev.Brand)
	}
	if dev.Model != "" {
		a = append(a, dev.Model)
	}
	if dev.Variant != "" {
		a = append(a, dev.Variant)
	}
	s = strings.Join(a, " ")
	return
}

func Identify(id string) (dev *Device) {
	dev = new(Device)
	if id == "" {
		return
	}

	if len(id) < 9 {
		return
	}
	switch id {
	case "18D1:4F00":
		dev.Set("Google", "Pixel ROM Recovery", "Tensor")
	}

	if len(id) < 20 {
		return
	}
	s := string(id[1:5])
	switch s {
	case "9845":
		dev.Set("Google", "Pixel 6 series", "")
	case "9855":
		dev.Set("Google", "Pixel 7 series", "")
	case "9865":
		dev.Set("Google", "Pixel 8 series", "")
	}

	return
}
