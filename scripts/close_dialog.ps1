Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class WinEnum {
  public delegate bool EnumProc(IntPtr hWnd, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc cb, IntPtr lParam);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder sb, int max);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
}
'@
$found = $null
$cb = [WinEnum+EnumProc]{
  param($h, $l)
  if ([WinEnum]::IsWindowVisible($h)) {
    $sb = New-Object System.Text.StringBuilder 256
    [WinEnum]::GetWindowText($h, $sb, 256) | Out-Null
    if ($sb.ToString() -like '*选择照片目录*') { $script:found = $h; return $false }
  }
  return $true
}
[WinEnum]::EnumWindows($cb, [IntPtr]::Zero) | Out-Null
if ($found) {
  Write-Output ("found: " + $found)
  [WinEnum]::SetForegroundWindow($found) | Out-Null
  Start-Sleep -Milliseconds 300
  [WinEnum]::PostMessage($found, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero) | Out-Null
  Write-Output 'closed'
} else {
  Write-Output 'not found'
}
