#!/usr/bin/env swift

import AppKit
import Foundation
import PDFKit

guard CommandLine.arguments.count >= 3 else {
    FileHandle.standardError.write(Data("usage: make_image_pdf.swift OUTPUT IMAGE...\n".utf8))
    exit(2)
}

let output = CommandLine.arguments[1]
let document = PDFDocument()

for (index, path) in CommandLine.arguments.dropFirst(2).enumerated() {
    guard let image = NSImage(contentsOfFile: path), let page = PDFPage(image: image) else {
        FileHandle.standardError.write(Data("failed to create PDF page from \(path)\n".utf8))
        exit(1)
    }
    document.insert(page, at: index)
}

guard document.write(to: URL(fileURLWithPath: output)) else {
    FileHandle.standardError.write(Data("failed to write \(output)\n".utf8))
    exit(1)
}

print("pages=\(document.pageCount) output=\(output)")
