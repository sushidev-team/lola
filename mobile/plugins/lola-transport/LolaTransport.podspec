require 'json'

package = JSON.parse(File.read(File.join(__dir__, 'package.json')))

# CocoaPods is the FALLBACK integration, not the primary one. See the comment at
# the top of Package.swift: Capacitor 8 made Swift Package Manager the default,
# so this file is only read by an app created with
# `npx cap add ios --packagemanager CocoaPods`. It must keep describing the same
# sources as Package.swift, which is why the glob covers both Swift targets in
# one pass — CocoaPods has no notion of the core/bridge split and does not need
# one, since it compiles the whole pod into a single module.
Pod::Spec.new do |s|
  s.name = 'LolaTransport'
  s.version = package['version']
  s.summary = package['description']
  s.license = package['license']
  s.homepage = 'https://github.com/sushidev-team/lola'
  s.author = 'sushi'
  s.source = { :git => 'https://github.com/sushidev-team/lola.git', :tag => s.version.to_s }
  s.source_files = 'ios/Sources/**/*.{swift,h,m,c,cc,mm,cpp}'
  s.ios.deployment_target = '15.0'
  s.dependency 'Capacitor'
  s.swift_version = '5.9'
end
