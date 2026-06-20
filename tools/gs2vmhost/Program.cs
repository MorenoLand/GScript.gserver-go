using Preagonal.Scripting.GS2Engine;
using Preagonal.Scripting.GS2Engine.Extensions;
using Preagonal.Scripting.GS2Engine.GS2.Script;
using Preagonal.Scripting.GS2Engine.Models;
using System.Text;
using System.Text.Json;
using static Preagonal.Scripting.GS2Engine.GS2.Script.Script;

namespace GServer.Go.Tools.GS2VmHost;

internal static class Program
{
	private static async Task<int> Main(string[] args)
	{
		if (args.Length < 3)
		{
			Console.Error.WriteLine("usage: gs2vmhost <scriptType> <scriptName> <eventName>");
			return 2;
		}
		var scriptType = args[0];
		var scriptName = args[1];
		var eventName = args[2];
		var eventArgs = args.Skip(3).ToArray();
		var scriptText = await Console.In.ReadToEndAsync();
		RegisterFunctions();
		try
		{
			Script.GlobalVariables.Clear();
			Script.GlobalVariables.AddOrUpdate("name", scriptName.ToStackEntry());
			Script.GlobalVariables.AddOrUpdate("params", string.Join(",", eventArgs).ToStackEntry());
			RegisterDataObjects();
			var response = GS2Compiler.Interface.CompileCode(scriptText, scriptType, scriptName);
			if (!response.Success)
			{
				Console.Error.WriteLine(string.IsNullOrWhiteSpace(response.ErrMsg) ? "compile failed" : response.ErrMsg);
				return 2;
			}
			var script = new Script(scriptName, response.ByteCode);
			await script.Call(eventName, eventArgs);
			return 0;
		}
		catch (Exception ex)
		{
			Console.WriteLine($"ERROR\t{ex.Message}");
			return 1;
		}
	}

	private static void RegisterFunctions()
	{
		Preagonal.Scripting.GS2Engine.Tools.DEBUG_ON = false;
		ScriptProperties<VmGlobals>.AddFunctions(
			null,
			new()
			{
				{ "echo", "", Echo },
				{ "printf", "", Echo },
				{ "triggerclient", "", TriggerClient },
			}
		);
		foreach (var property in GlobalProperties.Where(x => !x.Value.Compiled))
		{
			property.Value.Compile();
		}
	}

	private static int Echo(VmGlobals _, IStackEntry[] args)
	{
		if (args.Length > 0)
		{
			Console.WriteLine($"ECHO\t{args[0]?.GetValue()?.ToString() ?? ""}");
		}
		return 0;
	}

	private static int TriggerClient(VmGlobals _, IStackEntry[] args)
	{
		if (args.Length < 2)
		{
			return 0;
		}
		var type = args[0]?.GetValue()?.ToString() ?? "";
		if (type != "gui" && type != "weapon")
		{
			return 0;
		}
		var parts = args.Select(arg => (arg?.GetValue()?.ToString() ?? "").Replace("\t", " "));
		Console.WriteLine($"TRIGGERCLIENT\t{string.Join('\t', parts)}");
		return 0;
	}

	private static void RegisterDataObjects()
	{
		var flags = LoadEnvMap("GS2_SERVER_FLAGS");
		RegisterGlobalObject("server", BuildServerFlagObject(flags, "server."));
		RegisterGlobalObject("serverr", BuildServerFlagObject(flags, "serverr."));
		RegisterGlobalObject("serveroptions", BuildObject(LoadEnvMap("GS2_SERVER_OPTIONS")));
	}

	private static Dictionary<string, string> LoadEnvMap(string name)
	{
		var raw = Environment.GetEnvironmentVariable(name);
		if (string.IsNullOrWhiteSpace(raw))
		{
			return new();
		}
		try
		{
			var json = Encoding.UTF8.GetString(Convert.FromBase64String(raw));
			return JsonSerializer.Deserialize<Dictionary<string, string>>(json) ?? new();
		}
		catch
		{
			return new();
		}
	}

	private static void RegisterGlobalObject(string name, ScriptVariable obj)
	{
		Script.GlobalVariables.AddOrUpdate(name, obj.ToStackEntry());
		Script.GlobalObjects[name] = obj;
	}

	private static ScriptVariable BuildServerFlagObject(Dictionary<string, string> flags, string prefix)
	{
		var obj = new ScriptVariable();
		foreach (var (key, value) in flags)
		{
			var normalized = key.Trim().ToLowerInvariant();
			if (normalized.StartsWith(prefix, StringComparison.OrdinalIgnoreCase))
			{
				obj.AddOrUpdate(normalized[prefix.Length..], value.ToStackEntry());
			}
		}
		return obj;
	}

	private static ScriptVariable BuildObject(Dictionary<string, string> values)
	{
		var obj = new ScriptVariable();
		foreach (var (key, value) in values)
		{
			obj.AddOrUpdate(key.Trim().ToLowerInvariant(), value.ToStackEntry());
		}
		return obj;
	}
}

internal sealed class VmGlobals;
