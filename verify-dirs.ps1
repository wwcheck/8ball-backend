# 验证脚本：检查目录状态

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Sud8Ball 项目 - 目录状态验证" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# 检查主项目目录
$mainDir = "D:\美式八球\Sud8Ball-Backend"
Write-Host "1. 主项目目录:" -ForegroundColor Yellow
if (Test-Path $mainDir) {
    Write-Host "   ✅ 存在: $mainDir" -ForegroundColor Green
    $mainFiles = Get-ChildItem -Path $mainDir -File | Where-Object {$_.Extension -eq ".md"}
    Write-Host "   📄 文档文件: $($mainFiles.Count) 个"
} else {
    Write-Host "   ❌ 不存在" -ForegroundColor Red
}

Write-Host ""

# 检查原项目目录
$oldDir = "D:\美式八球\美式8球后端"
Write-Host "2. 原项目目录:" -ForegroundColor Yellow
if (Test-Path $oldDir) {
    Write-Host "   ❌ 仍然存在: $oldDir" -ForegroundColor Red
    Write-Host "   ⚠️ 需要手动删除" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "   💡 请手动执行:" -ForegroundColor Cyan
    Write-Host "   Remove-Item '$oldDir' -Recurse -Force" -ForegroundColor Gray
} else {
    Write-Host "   ✅ 已删除" -ForegroundColor Green
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "✅ 验证完成" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
