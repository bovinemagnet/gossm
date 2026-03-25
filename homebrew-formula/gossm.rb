class Gossm < Formula
  desc "CLI tool and daemon for managing AWS SSM sessions with an HTMX dashboard"
  homepage "https://github.com/bovinemagnet/gossm"
  version "0.0.9"
  license "GPL-3.0"

  if OS.mac?
    if Hardware::CPU.arm?
      url "https://github.com/bovinemagnet/gossm/releases/download/v#{version}/gossm_#{version}_Darwin_arm64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256_FOR_DARWIN_ARM64"
    else
      url "https://github.com/bovinemagnet/gossm/releases/download/v#{version}/gossm_#{version}_Darwin_x86_64v1.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256_FOR_DARWIN_AMD64"
    end
  elsif OS.linux?
    if Hardware::CPU.arm?
      url "https://github.com/bovinemagnet/gossm/releases/download/v#{version}/gossm_#{version}_Linux_arm64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256_FOR_LINUX_ARM64"
    else
      url "https://github.com/bovinemagnet/gossm/releases/download/v#{version}/gossm_#{version}_Linux_x86_64v1.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256_FOR_LINUX_AMD64"
    end
  end

  depends_on "awscli"

  def install
    bin.install "gossm"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/gossm version")
  end
end
