# tensor-usbdl

# Getting sources (linux)

To get the individual source files:

- Download factory image from https://developers.google.com/android/images
- Extract ZIP, copy device../bootloader-... file to sources/bootloader.img
- Use imjtool (https://newandroidbook.com/tools/imjtool.html) for unpacking the bootloader:
  - `imjtool sources/bootloader.img extract`
  - `for f in extracted/*; do mv $f sources/$(basename $f).img; done`

# Using the tool

```
go run ./cmd/tensor-usbdl
```
