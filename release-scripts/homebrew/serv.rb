class Serv < Formula
  desc "Programming language for background services, schedulers, and APIs"
  homepage "https://github.com/user/serv-lang"
  version "1.0.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/user/serv-lang/releases/download/v#{version}/serv-darwin-arm64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256"
    else
      url "https://github.com/user/serv-lang/releases/download/v#{version}/serv-darwin-amd64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/user/serv-lang/releases/download/v#{version}/serv-linux-arm64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256"
    else
      url "https://github.com/user/serv-lang/releases/download/v#{version}/serv-linux-amd64.tar.gz"
      sha256 "REPLACE_WITH_ACTUAL_SHA256"
    end
  end

  def install
    bin.install "serv"
    bin.install "serv-lsp"
  end

  test do
    # Verify the compiler runs
    assert_match "Serv: A Programming Language", shell_output("#{bin}/serv 2>&1", 0)
  end
end
