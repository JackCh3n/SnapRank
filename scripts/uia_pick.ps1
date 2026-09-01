Add-Type -AssemblyName UIAutomationClient
Add-Type -AssemblyName UIAutomationTypes

$root = [System.Windows.Automation.AutomationElement]::RootElement
$cond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, '选择照片目录')
$dlg = $null
for ($i = 0; $i -lt 15; $i++) {
  $dlg = $root.FindFirst([System.Windows.Automation.TreeScope]::Children, $cond)
  if ($dlg) { break }
  Start-Sleep -Milliseconds 500
}
if (-not $dlg) { Write-Output 'dialog-not-found'; exit 1 }
Write-Output 'dialog-found'

# 找到“文件夹名”编辑框并填入 C:\Windows
$editCond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::ControlTypeProperty, [System.Windows.Automation.ControlType]::Edit)
$edit = $dlg.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $editCond)
if ($edit) {
  $vp = $edit.GetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern)
  $vp.SetValue('C:\Windows')
  Write-Output 'path-set'
} else { Write-Output 'edit-not-found' }

# 点击“选择文件夹”按钮
$btnCond = New-Object System.Windows.Automation.PropertyCondition([System.Windows.Automation.AutomationElement]::NameProperty, '选择文件夹')
$btn = $dlg.FindFirst([System.Windows.Automation.TreeScope]::Descendants, $btnCond)
if ($btn) {
  $ip = $btn.GetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern)
  $ip.Invoke()
  Write-Output 'invoked'
} else { Write-Output 'btn-not-found' }
