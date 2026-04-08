// Harness that spawns the real Runner.Worker with a properly constructed job message.
// Uses the runner's own types for message construction to ensure wire format compatibility.
using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.IO.Pipes;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using GitHub.DistributedTask.Pipelines;
using GitHub.DistributedTask.WebApi;
using GitHub.Runner.Sdk;

class Program
{
    static async Task<int> Main(string[] args)
    {
        if (args.Length < 2)
        {
            Console.Error.WriteLine("Usage: harness <runner-bin-dir> <workflow-uses-spec>");
            Console.Error.WriteLine("  workflow-uses-spec: comma-separated uses: refs, e.g. 'actions/checkout@v4,actions/setup-go@v5'");
            Console.Error.WriteLine("");
            Console.Error.WriteLine("Environment:");
            Console.Error.WriteLine("  LAUNCH_ENDPOINT  Fake Launch URL (default: http://localhost:9399)");
            Console.Error.WriteLine("  GITHUB_TOKEN     Token for authentication");
            return 1;
        }

        var runnerBinDir = args[0];
        var usesSpecs = args[1].Split(',', StringSplitOptions.RemoveEmptyEntries);

        var launchEndpoint = Environment.GetEnvironmentVariable("LAUNCH_ENDPOINT") ?? "http://localhost:9399";
        var token = Environment.GetEnvironmentVariable("GITHUB_TOKEN") ?? "fake-token";

        var workerPath = Path.Combine(runnerBinDir, "Runner.Worker");
        if (!File.Exists(workerPath))
        {
            Console.Error.WriteLine($"Runner.Worker not found at {workerPath}");
            return 1;
        }

        // Build the job message using runner's own types
        var message = BuildMessage(usesSpecs, launchEndpoint, token);
        var messageJson = StringUtil.ConvertToJson(message);
        Console.Error.WriteLine($"Job message: {messageJson.Length} bytes, {message.Steps.Count} steps");
        if (Environment.GetEnvironmentVariable("DUMP_MSG") == "1") { Console.WriteLine(messageJson); return 0; }

        // Create anonymous pipes
        using var outServer = new AnonymousPipeServerStream(PipeDirection.Out, HandleInheritability.Inheritable);
        using var inServer = new AnonymousPipeServerStream(PipeDirection.In, HandleInheritability.Inheritable);

        var outHandle = outServer.GetClientHandleAsString();
        var inHandle = inServer.GetClientHandleAsString();

        Console.Error.WriteLine($"Starting worker: {workerPath} spawnclient {outHandle} {inHandle}");

        var psi = new ProcessStartInfo
        {
            FileName = workerPath,
            Arguments = $"spawnclient {outHandle} {inHandle}",
            WorkingDirectory = runnerBinDir,
            UseShellExecute = false,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };

        var process = Process.Start(psi)!;
        outServer.DisposeLocalCopyOfClientHandle();
        inServer.DisposeLocalCopyOfClientHandle();

        // Forward output
        _ = Task.Run(async () => { string? l; while ((l = await process.StandardOutput.ReadLineAsync()) != null) Console.WriteLine($"[WORKER] {l}"); });
        _ = Task.Run(async () => { string? l; while ((l = await process.StandardError.ReadLineAsync()) != null) Console.Error.WriteLine($"[WORKER:ERR] {l}"); });

        // Send job message using the EXACT same wire protocol as ProcessChannel/StreamString
        // CRITICAL: StreamString uses UnicodeEncoding (UTF-16LE), NOT UTF-8!
        Console.Error.WriteLine("Sending job message...");
        var bodyBytes = Encoding.Unicode.GetBytes(messageJson);  // UTF-16LE!
        await WriteInt32Async(outServer, 1); // MessageType.NewJobRequest
        await WriteInt32Async(outServer, bodyBytes.Length);
        await outServer.WriteAsync(bodyBytes, 0, bodyBytes.Length);
        await outServer.FlushAsync();
        Console.Error.WriteLine("Job message sent. Waiting for worker...");

        // Read worker responses in background
        _ = Task.Run(async () =>
        {
            try
            {
                while (true)
                {
                    var msgType = await ReadInt32Async(inServer);
                    var bodyLen = await ReadInt32Async(inServer);
                    var buf = new byte[bodyLen];
                    int read = 0;
                    while (read < bodyLen) { var n = await inServer.ReadAsync(buf, read, bodyLen - read); if (n == 0) break; read += n; }
                    Console.Error.WriteLine($"[WORKER->HARNESS] type={msgType} body={Encoding.UTF8.GetString(buf, 0, Math.Min(read, 200))}...");
                }
            }
            catch { /* pipe closed */ }
        });

        await process.WaitForExitAsync();
        Console.Error.WriteLine($"Worker exited with code {process.ExitCode}");
        return process.ExitCode;
    }

    static AgentJobRequestMessage BuildMessage(string[] usesSpecs, string launchEndpoint, string token)
    {
        var plan = new TaskOrchestrationPlanReference();
        var timeline = new TimelineReference { Id = Guid.NewGuid() };
        var jobId = Guid.NewGuid();

        var steps = new List<ActionStep>();
        for (int i = 0; i < usesSpecs.Length; i++)
        {
            var spec = usesSpecs[i].Trim();
            var step = ParseUsesSpec(spec, i);
            if (step != null) steps.Add(step);
        }

        var variables = new Dictionary<string, VariableValue>
        {
            ["system.culture"] = "en-US",
            ["system.github.launch_endpoint"] = launchEndpoint,
            ["system.github.job"] = "test-job",
            ["system.github.workspace"] = "/tmp/actions-workspace",
            ["system.github.token"] = new VariableValue(token, true),
        };

        // If LOCKFILE_DEPS env var is set, pass it as the dependencies variable
        var lockfileDeps = Environment.GetEnvironmentVariable("LOCKFILE_DEPS");
        if (!string.IsNullOrEmpty(lockfileDeps))
        {
            variables["system.actions.dependencies"] = lockfileDeps;
        }

        var resources = new JobResources();
        resources.Endpoints.Add(new ServiceEndpoint
        {
            Name = WellKnownServiceEndpointNames.SystemVssConnection,
            Url = new Uri(launchEndpoint),
            Authorization = new EndpointAuthorization
            {
                Scheme = "OAuth",
                Parameters = { { "AccessToken", token } }
            }
        });
        resources.Repositories.Add(new RepositoryResource
        {
            Alias = PipelineConstants.SelfAlias,
            Id = "github",
            Version = "sha1"
        });

        var contextData = new GitHub.DistributedTask.Pipelines.ContextData.DictionaryContextData();
        var githubContext = new GitHub.DistributedTask.Pipelines.ContextData.DictionaryContextData();
        githubContext.Add("repository", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("nodeselector/actions-test-fixtures"));
        githubContext.Add("repository_owner", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("nodeselector"));
        githubContext.Add("workspace", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("/tmp/actions-workspace"));
        githubContext.Add("sha", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("abc123"));
        githubContext.Add("ref", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("refs/heads/main"));
        githubContext.Add("server_url", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("https://github.com"));
        githubContext.Add("api_url", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("https://api.github.com"));
        githubContext.Add("action_path", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData(""));
        githubContext.Add("event_name", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("workflow_dispatch"));
        githubContext.Add("event", new GitHub.DistributedTask.Pipelines.ContextData.DictionaryContextData());
        githubContext.Add("repository_id", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("repository_owner_id", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("actor", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("test"));
        githubContext.Add("actor_id", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("workflow", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("test"));
        githubContext.Add("run_id", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("run_number", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("run_attempt", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("1"));
        githubContext.Add("head_ref", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData(""));
        githubContext.Add("base_ref", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData(""));
        githubContext.Add("ref_name", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("main"));
        githubContext.Add("ref_type", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("branch"));
        githubContext.Add("repository_visibility", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("public"));
        githubContext.Add("graphql_url", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("https://api.github.com/graphql"));
        githubContext.Add("retention_days", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("90"));
        githubContext.Add("artifact_cache_size_limit", new GitHub.DistributedTask.Pipelines.ContextData.StringContextData("10"));
        contextData.Add("github", githubContext);

        var message = new AgentJobRequestMessage(
            plan, timeline, jobId,
            "test-job", "test-job",
            null, null, null,
            variables, new List<MaskHint>(),
            resources, contextData,
            new WorkspaceOptions(), steps,
            null, null, null,
            new ActionsEnvironmentReference("production"),
            null,
            messageType: "RunnerJobRequest");

        return message;
    }

    static ActionStep? ParseUsesSpec(string spec, int index)
    {
        // owner/repo(/path)?@ref
        var atParts = spec.Split('@', 2);
        if (atParts.Length != 2) return null;
        var gitRef = atParts[1];
        var segments = atParts[0].Split('/', 3);
        if (segments.Length < 2) return null;

        var name = $"{segments[0]}/{segments[1]}";
        var path = segments.Length > 2 ? segments[2] : "";

        var step = new ActionStep
        {
            Id = Guid.NewGuid(),
            DisplayName = spec,
            Condition = "success()",
            Reference = new RepositoryPathReference
            {
                Name = name,
                Ref = gitRef,
                Path = path,
                RepositoryType = "GitHub",
            }
        };
        return step;
    }

    static async Task WriteInt32Async(Stream stream, int value)
    {
        await stream.WriteAsync(BitConverter.GetBytes(value));
    }

    static async Task<int> ReadInt32Async(Stream stream)
    {
        var bytes = new byte[4];
        int read = 0;
        while (read < 4) { var n = await stream.ReadAsync(bytes, read, 4 - read); if (n == 0) throw new EndOfStreamException(); read += n; }
        return BitConverter.ToInt32(bytes, 0);
    }
}
